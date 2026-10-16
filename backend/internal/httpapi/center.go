package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"

	"railway-map-backend/internal/geo"
	"railway-map-backend/internal/model"
	"railway-map-backend/internal/pluginconfig"
	"railway-map-backend/internal/rbac"
	"railway-map-backend/internal/settings"
	"railway-map-backend/internal/store"
)

// Center 是个人中心（/api/v1/me/*、/api/v1/admin/*）的 handler 集合。
// 每个受保护接口都在后端独立校验登录与权限，前端菜单可见性只是体验层。
type Center struct {
	api      *API
	store    *store.Store
	cache    *geo.Cache
	rbac     *rbac.Service
	settings *settings.Service
	plugin   *pluginconfig.Requester
	online   func() bool
	logger   *slog.Logger
	// onSettingsChanged 在服务器配置保存 / 重置后调用（作废寻路结果缓存等）。
	onSettingsChanged func()
}

// CenterOptions 聚合构建 Center 所需依赖。
type CenterOptions struct {
	API               *API
	Store             *store.Store
	Cache             *geo.Cache
	RBAC              *rbac.Service
	Settings          *settings.Service
	Plugin            *pluginconfig.Requester
	PluginOnline      func() bool
	OnSettingsChanged func()
	Logger            *slog.Logger
}

// NewCenter 创建个人中心 handler。
func NewCenter(o CenterOptions) *Center {
	return &Center{
		api: o.API, store: o.Store, cache: o.Cache, rbac: o.RBAC, settings: o.Settings,
		plugin: o.Plugin, online: o.PluginOnline, logger: o.Logger, onSettingsChanged: o.OnSettingsChanged,
	}
}

// --- 鉴权中间件 ---

type ctxKey int

const (
	ctxPlayer ctxKey = iota
	ctxPerms
)

// RequireLogin 要求已登录；把玩家与其权限放入 context。
func (c *Center) RequireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		player := c.api.requirePlayer(w, r)
		if player == nil {
			return
		}
		player.UUID = rbac.NormalizeUUID(player.UUID)
		perms, err := c.rbac.Permissions(player.UUID)
		if err != nil {
			c.logger.Warn("Failed to load permissions", "err", err, "player", player.UUID)
			writeError(w, http.StatusInternalServerError, "internal-error", "读取权限失败")
			return
		}
		ctx := context.WithValue(r.Context(), ctxPlayer, player)
		ctx = context.WithValue(ctx, ctxPerms, perms)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePerm 要求拥有某权限（须在 RequireLogin 之后）。
func (c *Center) RequirePerm(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !hasPerm(r, perm) {
				writeError(w, http.StatusForbidden, "forbidden", "没有权限")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func playerOf(r *http.Request) *model.Player {
	p, _ := r.Context().Value(ctxPlayer).(*model.Player)
	return p
}

func permsOf(r *http.Request) []string {
	p, _ := r.Context().Value(ctxPerms).([]string)
	return p
}

func hasPerm(r *http.Request, perm string) bool {
	return slices.Contains(permsOf(r), perm)
}

// --- 公共工具 ---

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "bad-request", "请求体无效")
		return false
	}
	return true
}

// audit 记录一条操作日志（失败只打日志，不影响业务）。detail 会序列化为 JSON。
func (c *Center) audit(r *http.Request, action, target string, detail any, success bool, reason string) {
	p := playerOf(r)
	if p == nil {
		return
	}
	var detailJSON string
	if detail != nil {
		if b, err := json.Marshal(detail); err == nil {
			detailJSON = string(b)
		}
	}
	if err := c.store.InsertAudit(store.AuditEntry{
		ActorUUID: p.UUID, ActorName: p.Name, Action: action, Target: target,
		Detail: detailJSON, Success: success, Reason: reason,
	}); err != nil {
		c.logger.Warn("Failed to write audit log", "err", err, "action", action)
	}
}

// pluginCall 执行一次插件配置操作，统一处理离线 / 超时 / 拒绝，记录操作日志，并写出响应。
// 成功时返回 true（响应体为 {ok, warnings}）。
func (c *Center) pluginCall(w http.ResponseWriter, r *http.Request, op, action, target string, payload, detail any) bool {
	res, err := c.plugin.Do(op, *playerOf(r), payload)
	switch {
	case errors.Is(err, pluginconfig.ErrPluginOffline):
		c.audit(r, action, target, detail, false, "plugin-offline")
		writeError(w, http.StatusServiceUnavailable, "plugin-offline", "游戏服务器未连接，暂时无法修改")
		return false
	case errors.Is(err, pluginconfig.ErrTimeout):
		c.audit(r, action, target, detail, false, "timeout")
		writeError(w, http.StatusGatewayTimeout, "plugin-timeout", "游戏服务器响应超时，请刷新后确认是否已生效")
		return false
	case err != nil:
		c.logger.Warn("Plugin config request failed", "err", err, "op", op)
		writeError(w, http.StatusInternalServerError, "internal-error", "请求失败")
		return false
	}
	if !res.Success {
		c.audit(r, action, target, detail, false, res.Reason)
		msg := res.Message
		if msg == "" {
			msg = "游戏服务器拒绝了该修改：" + res.Reason
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":     model.APIErrorBody{Code: orDefault(res.Reason, "rejected"), Message: msg},
			"field":     res.Field,
			"conflicts": res.Conflicts,
		})
		return false
	}
	c.audit(r, action, target, detail, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "warnings": res.Warnings})
	return true
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Profile 返回当前玩家信息、权限与侧栏可见的铁路系统（GET /api/v1/me/profile）。
func (c *Center) Profile(w http.ResponseWriter, r *http.Request) {
	p := playerOf(r)
	type sysItem struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		IsCreator bool   `json:"isCreator"`
		IsMember  bool   `json:"isMember"`
		LogoURL   string `json:"logoUrl,omitempty"`
	}
	manageAll := hasPerm(r, rbac.PermSystemManage)
	var mine, others []sysItem
	for _, s := range c.cache.PrivateSystems() {
		member := slices.Contains(s.Members, p.UUID) || s.Creator == p.UUID
		if !member && !manageAll {
			continue
		}
		item := sysItem{ID: s.ID, Name: s.Name, IsCreator: s.Creator == p.UUID, IsMember: member}
		if s.LogoURL != nil {
			item.LogoURL = *s.LogoURL
		}
		if member {
			mine = append(mine, item)
		} else {
			others = append(others, item)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"player":       p,
		"permissions":  permsOf(r),
		"systems":      append(append([]sysItem{}, mine...), others...),
		"pluginOnline": c.online(),
	})
}
