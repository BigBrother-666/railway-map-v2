// Package httpapi 提供对前端的 REST 接口（见 docs/BACKEND_PROMPT.md §5）。
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"railway-map-backend/internal/auth"
	"railway-map-backend/internal/config"
	"railway-map-backend/internal/geo"
	"railway-map-backend/internal/model"
	"railway-map-backend/internal/purchase"
	"railway-map-backend/internal/realtime"
)

// PluginStatus 暴露插件连通状态（由 pluginlink.Server 实现）。
type PluginStatus interface {
	Online() bool
}

// API 聚合 REST handler 所需依赖。
type API struct {
	cache    *geo.Cache
	agg      *realtime.Aggregator
	plugin   PluginStatus
	purchase *purchase.Orchestrator
	auth     *auth.Service
	store    DataStore
	frontend model.FrontendBootstrap
	// frontendBaseURL 是 OAuth 回调完成后跳转回的地图页面地址（带 ?login=... 提示）。
	frontendBaseURL string
	logger          *slog.Logger
}

// DataStore records purchases and ride history (implemented by store.Store).
type DataStore interface {
	LogPurchase(requestID, playerUUID, playerName, nodeIDs string, success bool, reason string, price float64) error
	ListRideHistory(playerUUID string, page, pageSize int) (model.RideHistoryResponse, error)
}

// Options 聚合构建 API 所需依赖。
type Options struct {
	Cache           *geo.Cache
	Agg             *realtime.Aggregator
	Plugin          PluginStatus
	Purchase        *purchase.Orchestrator
	Auth            *auth.Service
	Store           DataStore
	Frontend        config.FrontendConfig
	FrontendBaseURL string
	TestAuthEnabled bool
	TestAuthUUIDs   []string
	Logger          *slog.Logger
}

// New 创建 API。
func New(o Options) *API {
	return &API{
		cache:    o.Cache,
		agg:      o.Agg,
		plugin:   o.Plugin,
		purchase: o.Purchase,
		auth:     o.Auth,
		store:    o.Store,
		frontend: model.FrontendBootstrap{
			FrontendConfig:  o.Frontend,
			TestAuthEnabled: o.TestAuthEnabled,
			TestAuthUUIDs:   o.TestAuthUUIDs,
		},
		frontendBaseURL: o.FrontendBaseURL,
		logger:          o.Logger,
	}
}

// --- 公共数据 ---

// Health 存活探针。
func (a *API) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) Config(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.frontend)
}

// Meta 返回 geo 版本、插件在线状态、服务器时间。
func (a *API) Meta(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, model.Meta{
		GeoVersion: a.cache.GeoVersion(),
		Online:     a.plugin.Online(),
		ServerTime: time.Now().UnixMilli(),
	})
}

// Geojson 返回合并后的 FeatureCollection（带 version 头，支持 ETag 协商）。
func (a *API) Geojson(w http.ResponseWriter, r *http.Request) {
	payload, version := a.cache.Geojson()
	if payload == nil {
		writeError(w, http.StatusNotFound, "no-geo", "geojson 尚未就绪")
		return
	}
	if match := r.Header.Get("If-None-Match"); match != "" && match == version {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", version)
	w.Header().Set("X-Geo-Version", version)
	writeRaw(w, http.StatusOK, payload)
}

// Lines / Systems 返回线路 / 系统数组（原始 JSON 透传）。
func (a *API) Lines(w http.ResponseWriter, r *http.Request) {
	if b := a.cache.Lines(); b != nil {
		writeRaw(w, http.StatusOK, b)
		return
	}
	writeRaw(w, http.StatusOK, []byte("[]"))
}

func (a *API) Systems(w http.ResponseWriter, r *http.Request) {
	if b := a.cache.Systems(); b != nil {
		writeRaw(w, http.StatusOK, b)
		return
	}
	writeRaw(w, http.StatusOK, []byte("[]"))
}

// Trains 返回当前所有列车快照（WS 之外的一次性拉取）。
func (a *API) Trains(w http.ResponseWriter, r *http.Request) {
	snapshot := a.agg.Snapshot()
	if snapshot == nil {
		snapshot = []json.RawMessage{}
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// --- 鉴权 ---

// Login 启动微软登录（302 跳转）。
func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil || !a.auth.OAuthConfigured() {
		writeError(w, http.StatusServiceUnavailable, "auth-disabled", "登录未配置")
		return
	}
	state := randomState()
	http.SetCookie(w, &http.Cookie{
		Name: "bcts_oauth_state", Value: state, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(10 * time.Minute),
	})
	http.Redirect(w, r, a.auth.AuthCodeURL(state), http.StatusFound)
}

// Callback 处理 OAuth 回调：校验 state、换取 profile、校验白名单、签发会话。
func (a *API) Callback(w http.ResponseWriter, r *http.Request) {
	if a.auth == nil || !a.auth.OAuthConfigured() {
		writeError(w, http.StatusServiceUnavailable, "auth-disabled", "登录未配置")
		return
	}
	stateCookie, err := r.Cookie("bcts_oauth_state")
	if err != nil || r.URL.Query().Get("state") != stateCookie.Value {
		a.redirectLogin(w, r, "error", "bad-state")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		a.redirectLogin(w, r, "error", "no-code")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	player, err := a.auth.Exchange(ctx, code)
	if err != nil {
		if err == auth.ErrNotBound {
			a.logger.Warn("Login rejected for unbound player")
			a.redirectLogin(w, r, "error", "not-bound")
			return
		}
		a.logger.Warn("Login failed", "err", err)
		a.redirectLogin(w, r, "error", "login-failed")
		return
	}
	if err := a.auth.IssueCookie(w, player); err != nil {
		a.logger.Warn("Failed to issue session cookie", "err", err, "player", player.UUID)
		a.redirectLogin(w, r, "error", "session-failed")
		return
	}
	a.logger.Info("Login completed", "player", player.UUID, "name", player.Name)
	a.redirectLogin(w, r, "success", "")
}

// redirectLogin 302 跳转回前端地图页，用 ?login=success|error（失败附 &reason=）告知结果，
// 由前端读取后弹出提示并清理 URL。避免把原始 JSON 直接暴露给用户。
func (a *API) redirectLogin(w http.ResponseWriter, r *http.Request, status, reason string) {
	base := strings.TrimRight(a.frontendBaseURL, "/") // 空则兜底为同源根路径
	q := url.Values{}
	q.Set("login", status)
	if reason != "" {
		q.Set("reason", reason)
	}
	http.Redirect(w, r, base+"/?"+q.Encode(), http.StatusFound)
}

func (a *API) TestLogin(w http.ResponseWriter, r *http.Request) {
	if !a.frontend.TestAuthEnabled {
		writeError(w, http.StatusNotFound, "test-auth-disabled", "测试登录未启用")
		return
	}
	if a.auth == nil {
		writeError(w, http.StatusServiceUnavailable, "auth-disabled", "登录未配置")
		return
	}
	var req model.TestLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.UUID == "" {
		writeError(w, http.StatusBadRequest, "bad-request", "请求体无效")
		return
	}
	allowed := false
	for _, uuid := range a.frontend.TestAuthUUIDs {
		if uuid == req.UUID {
			allowed = true
			break
		}
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "not-allowed", "该 UUID 未在测试登录列表中")
		return
	}
	player := &model.Player{UUID: req.UUID, Name: "Test-" + shortUUID(req.UUID)}
	if err := a.auth.IssueCookie(w, player); err != nil {
		a.logger.Warn("Failed to issue test session cookie", "err", err, "player", player.UUID)
		writeError(w, http.StatusInternalServerError, "session-failed", "签发会话失败")
		return
	}
	a.logger.Info("Test login completed", "player", player.UUID)
	writeJSON(w, http.StatusOK, player)
}

// Me 返回当前登录玩家。
func (a *API) Me(w http.ResponseWriter, r *http.Request) {
	player := a.requirePlayer(w, r)
	if player == nil {
		return
	}
	writeJSON(w, http.StatusOK, player)
}

func (a *API) MyRideHistory(w http.ResponseWriter, r *http.Request) {
	player := a.requirePlayer(w, r)
	if player == nil {
		return
	}
	if a.store == nil {
		writeError(w, http.StatusServiceUnavailable, "history-disabled", "乘车历史不可用")
		return
	}
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	pageSize := parsePositiveInt(r.URL.Query().Get("pageSize"), 10)
	resp, err := a.store.ListRideHistory(player.UUID, page, pageSize)
	if err != nil {
		a.logger.Warn("Failed to read ride history", "err", err)
		writeError(w, http.StatusInternalServerError, "history-failed", "读取乘车历史失败")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// Logout 注销会话。
func (a *API) Logout(w http.ResponseWriter, r *http.Request) {
	if a.auth != nil {
		a.auth.ClearCookie(w)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- 购票 ---

// Purchase 处理在线购票（需登录）。
func (a *API) Purchase(w http.ResponseWriter, r *http.Request) {
	player := a.requirePlayer(w, r)
	if player == nil {
		return
	}
	var req model.PurchaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad-request", "请求体无效")
		return
	}
	if len(req.NodeIDs) < 2 {
		writeError(w, http.StatusBadRequest, "bad-route", "路线节点不足")
		return
	}
	if a.purchase == nil || !a.plugin.Online() {
		a.logger.Warn("Purchase rejected because plugin is offline", "player", player.UUID)
		writeError(w, http.StatusServiceUnavailable, "plugin-offline", "游戏服务器暂时无法购票")
		return
	}
	a.logger.Info("Purchase request submitted", "player", player.UUID, "nodes", len(req.NodeIDs))
	result := a.purchase.Submit(player.UUID, player.Name, req)
	if a.store != nil {
		nodeIDs, _ := json.Marshal(req.NodeIDs)
		_ = a.store.LogPurchase(result.RequestID, player.UUID, player.Name, string(nodeIDs),
			result.Success, result.Reason, result.Price)
	}
	a.logger.Info("Purchase request completed", "requestId", result.RequestID, "player", player.UUID, "success", result.Success, "reason", result.Reason, "price", result.Price)
	writeJSON(w, http.StatusOK, result)
}

// requirePlayer 从会话取玩家，未登录写 401 并返回 nil。
func (a *API) requirePlayer(w http.ResponseWriter, r *http.Request) *model.Player {
	if a.auth == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "未登录")
		return nil
	}
	player, err := a.auth.PlayerFromRequest(r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "未登录")
		return nil
	}
	return player
}

func parsePositiveInt(s string, fallback int) int {
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return fallback
	}
	return v
}

func shortUUID(uuid string) string {
	if len(uuid) >= 8 {
		return uuid[:8]
	}
	return uuid
}
