package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/geo"
	"railway-map-backend/internal/model"
	"railway-map-backend/internal/rbac"
	"railway-map-backend/internal/settings"
	"railway-map-backend/internal/store"
	"railway-map-backend/internal/tilesource"
)

// --- 概览 ---

// systemOverview 是概览页中某铁路系统的统计。
type systemOverview struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	IsCreator    bool             `json:"isCreator"`
	Income       float64          `json:"income"`
	Withdrawn    float64          `json:"withdrawn"`
	LineCount    int              `json:"lineCount"`
	StationCount int              `json:"stationCount"`
	TrackKm      float64          `json:"trackKm"`
	DailyRevenue []store.DayValue `json:"dailyRevenue"`
	DailyRiders  []store.DayValue `json:"dailyRiders"`
	RevenueSince int64            `json:"revenueSince,omitempty"`
}

// Overview 返回个人统计与所在铁路系统统计（GET /me/overview）。
func (c *Center) Overview(w http.ResponseWriter, r *http.Request) {
	p := playerOf(r)
	stats, err := c.store.PlayerStats(p.UUID)
	if err != nil {
		c.logger.Warn("Failed to compute player stats", "err", err, "player", p.UUID)
		writeError(w, http.StatusInternalServerError, "stats-failed", "读取统计失败")
		return
	}
	lineNames := map[string]string{}
	for _, l := range c.cache.PrivateLines() {
		lineNames[l.ID] = l.Name
	}
	var geoStats map[string]geo.SystemGeoStats
	systems := []systemOverview{}
	for _, sys := range c.cache.PrivateSystems() {
		if sys.Creator != p.UUID && !slices.Contains(sys.Members, p.UUID) {
			continue
		}
		if geoStats == nil {
			geoStats = c.cache.SystemGeoStats()
		}
		var lineIDs []string
		for _, l := range c.cache.PrivateLines() {
			if l.SystemID == sys.ID {
				lineIDs = append(lineIDs, l.ID)
			}
		}
		revenue, err := c.store.SystemDailyRevenue(sys.ID, 30)
		if err != nil {
			c.logger.Warn("Failed to compute system revenue", "err", err, "system", sys.ID)
			writeError(w, http.StatusInternalServerError, "stats-failed", "读取统计失败")
			return
		}
		riders, err := c.store.LinesDailyRidership(lineIDs, 14)
		if err != nil {
			c.logger.Warn("Failed to compute ridership", "err", err, "system", sys.ID)
			writeError(w, http.StatusInternalServerError, "stats-failed", "读取统计失败")
			return
		}
		gs := geoStats[sys.ID]
		systems = append(systems, systemOverview{
			ID: sys.ID, Name: sys.Name, IsCreator: sys.Creator == p.UUID,
			Income: sys.Income, Withdrawn: sys.Withdrawn, LineCount: len(lineIDs),
			StationCount: gs.StationCount, TrackKm: gs.TrackKm,
			DailyRevenue: revenue, DailyRiders: riders,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"player":    p,
		"stats":     stats,
		"lineNames": lineNames,
		"systems":   systems,
	})
}

// --- 权限管理 ---

// RBACOverview 返回权限点、角色与用户授权（GET /admin/rbac）。
func (c *Center) RBACOverview(w http.ResponseWriter, r *http.Request) {
	roles, err := c.store.ListRoles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal-error", "读取角色失败")
		return
	}
	users, err := c.store.ListUserRoles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal-error", "读取授权失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"permissions":    rbac.AllPermissions,
		"roles":          roles,
		"users":          users,
		"superAdminUuid": c.rbac.SuperAdminUUID(),
		"superRoleId":    c.rbac.SuperRoleID(),
	})
}

type roleRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

func (c *Center) writeRBACError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not-found", "角色不存在")
	case errors.Is(err, rbac.ErrBuiltinRole):
		writeError(w, http.StatusBadRequest, "builtin-role", "内置角色不可修改或删除")
	case errors.Is(err, rbac.ErrUnknownPerm):
		writeError(w, http.StatusBadRequest, "unknown-permission", "包含未知权限")
	case errors.Is(err, rbac.ErrBadName):
		writeFieldError(w, "name", "角色名不能为空、不超过 32 个字符，且不能为 super_admin")
	case errors.Is(err, rbac.ErrProtectedUser):
		writeError(w, http.StatusBadRequest, "protected", "初始超级管理员（环境变量指定）不可撤销")
	case errors.Is(err, rbac.ErrSelfLockout):
		writeError(w, http.StatusBadRequest, "self-lockout", "该操作会使你失去权限管理权限")
	case err != nil && strings.Contains(strings.ToLower(err.Error()), "unique"),
		err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate"):
		writeFieldError(w, "name", "角色名已存在")
	default:
		c.logger.Warn("RBAC operation failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal-error", "操作失败")
	}
}

// CreateRole 新建角色（POST /admin/rbac/roles）。
func (c *Center) CreateRole(w http.ResponseWriter, r *http.Request) {
	var req roleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := c.rbac.CreateRole(req.Name, req.Description, req.Permissions)
	if err != nil {
		c.writeRBACError(w, err)
		return
	}
	c.audit(r, "rbac.role.create", req.Name, req, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func roleIDParam(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "roleId"), 10, 64)
	return id, err == nil && id > 0
}

// UpdateRole 修改角色（PUT /admin/rbac/roles/{roleId}）。
func (c *Center) UpdateRole(w http.ResponseWriter, r *http.Request) {
	id, ok := roleIDParam(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not-found", "角色不存在")
		return
	}
	var req roleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	before, _ := c.store.RoleByID(id)
	if err := c.rbac.UpdateRole(playerOf(r).UUID, id, req.Name, req.Description, req.Permissions); err != nil {
		c.writeRBACError(w, err)
		return
	}
	c.audit(r, "rbac.role.update", req.Name, map[string]any{"before": before, "after": req}, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// DeleteRole 删除角色（DELETE /admin/rbac/roles/{roleId}）。
func (c *Center) DeleteRole(w http.ResponseWriter, r *http.Request) {
	id, ok := roleIDParam(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not-found", "角色不存在")
		return
	}
	before, _ := c.store.RoleByID(id)
	if err := c.rbac.DeleteRole(playerOf(r).UUID, id); err != nil {
		c.writeRBACError(w, err)
		return
	}
	target := strconv.FormatInt(id, 10)
	if before != nil {
		target = before.Name
	}
	c.audit(r, "rbac.role.delete", target, map[string]any{"before": before}, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// GrantRole 授予角色（POST /admin/rbac/users，body {uuid, roleId}）。
func (c *Center) GrantRole(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UUID   string `json:"uuid"`
		RoleID int64  `json:"roleId"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	uuid := rbac.NormalizeUUID(req.UUID)
	if !uuidPattern.MatchString(uuid) {
		writeFieldError(w, "uuid", "UUID 格式无效")
		return
	}
	if err := c.rbac.Grant(playerOf(r).UUID, uuid, req.RoleID); err != nil {
		c.writeRBACError(w, err)
		return
	}
	c.audit(r, "rbac.user.grant", uuid, req, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// RevokeRole 撤销角色（DELETE /admin/rbac/users/{uuid}/roles/{roleId}）。
func (c *Center) RevokeRole(w http.ResponseWriter, r *http.Request) {
	id, ok := roleIDParam(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not-found", "角色不存在")
		return
	}
	uuid := rbac.NormalizeUUID(chi.URLParam(r, "uuid"))
	if err := c.rbac.Revoke(playerOf(r).UUID, uuid, id); err != nil {
		c.writeRBACError(w, err)
		return
	}
	c.audit(r, "rbac.user.revoke", uuid, map[string]any{"roleId": id}, true, "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// --- 服务器配置 ---

// GetSettings 返回当前配置、默认值与修改信息（GET /admin/settings）。
func (c *Center) GetSettings(w http.ResponseWriter, r *http.Request) {
	cur := c.settings.Current()
	writeJSON(w, http.StatusOK, map[string]any{
		"frontend":     cur.Frontend,
		"route":        cur.Route,
		"plugin":       cur.Plugin,
		"realtime":     cur.Realtime,
		"defaults":     settings.Defaults(),
		"frontendMeta": cur.FrontendMeta,
		"routeMeta":    cur.RouteMeta,
		"pluginMeta":   cur.PluginMeta,
		"realtimeMeta": cur.RealtimeMeta,
		"tiles":        cur.Tiles,
		"tilesMeta":    cur.TilesMeta,
	})
}

// TileSourcesStatus 返回外部地图来源的发现状态与世界匹配情况（GET /admin/settings/tiles/status）。
func (c *Center) TileSourcesStatus(w http.ResponseWriter, r *http.Request) {
	if c.api.tiles == nil {
		writeJSON(w, http.StatusOK, tilesource.Status{Sources: []tilesource.SourceStatus{}})
		return
	}
	writeJSON(w, http.StatusOK, c.api.tiles.Status())
}

// UpdateSettings 整段保存一段配置（PUT /admin/settings/{section}）。
func (c *Center) UpdateSettings(w http.ResponseWriter, r *http.Request) {
	section := chi.URLParam(r, "section")
	actor := playerOf(r).UUID
	cur := c.settings.Current()
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20) // 列车图标等可能是较大的 data URL
	var err error
	var before, after any
	switch section {
	case settings.SectionFrontend:
		var f config.FrontendConfig
		if json.NewDecoder(r.Body).Decode(&f) != nil {
			writeError(w, http.StatusBadRequest, "bad-request", "请求体无效")
			return
		}
		before, after = cur.Frontend, f
		err = c.settings.UpdateFrontend(f, actor)
	case settings.SectionRoute:
		var rc config.RouteConfig
		if json.NewDecoder(r.Body).Decode(&rc) != nil {
			writeError(w, http.StatusBadRequest, "bad-request", "请求体无效")
			return
		}
		before, after = cur.Route, rc
		err = c.settings.UpdateRoute(rc, actor)
	case settings.SectionPlugin:
		var pc config.PluginSettings
		if json.NewDecoder(r.Body).Decode(&pc) != nil {
			writeError(w, http.StatusBadRequest, "bad-request", "请求体无效")
			return
		}
		before, after = cur.Plugin, pc
		err = c.settings.UpdatePlugin(pc, actor)
	case settings.SectionRealtime:
		var rt config.RealtimeConfig
		if json.NewDecoder(r.Body).Decode(&rt) != nil {
			writeError(w, http.StatusBadRequest, "bad-request", "请求体无效")
			return
		}
		before, after = cur.Realtime, rt
		err = c.settings.UpdateRealtime(rt, actor)
	case settings.SectionTiles:
		var tc config.TilesConfig
		if json.NewDecoder(r.Body).Decode(&tc) != nil {
			writeError(w, http.StatusBadRequest, "bad-request", "请求体无效")
			return
		}
		before, after = cur.Tiles, tc
		err = c.settings.UpdateTiles(tc, actor)
	default:
		writeError(w, http.StatusNotFound, "not-found", "未知的配置段")
		return
	}
	var fieldErrs settings.FieldErrors
	if errors.As(err, &fieldErrs) {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  model.APIErrorBody{Code: "invalid", Message: "配置校验失败"},
			"fields": fieldErrs,
		})
		return
	}
	if err != nil {
		c.logger.Warn("Failed to save settings", "err", err, "section", section)
		writeError(w, http.StatusInternalServerError, "internal-error", "保存失败")
		return
	}
	if c.onSettingsChanged != nil {
		c.onSettingsChanged()
	}
	c.audit(r, "settings.update", section, map[string]any{"before": before, "after": after}, true, "")
	c.GetSettings(w, r)
}

// ResetSettings 恢复某段配置为默认值（POST /admin/settings/{section}/reset）。
func (c *Center) ResetSettings(w http.ResponseWriter, r *http.Request) {
	section := chi.URLParam(r, "section")
	if err := c.settings.Reset(section); err != nil {
		if errors.Is(err, settings.ErrUnknownSection) {
			writeError(w, http.StatusNotFound, "not-found", "未知的配置段")
			return
		}
		c.logger.Warn("Failed to reset settings", "err", err, "section", section)
		writeError(w, http.StatusInternalServerError, "internal-error", "恢复默认失败")
		return
	}
	if c.onSettingsChanged != nil {
		c.onSettingsChanged()
	}
	c.audit(r, "settings.reset", section, nil, true, "")
	c.GetSettings(w, r)
}

// --- 操作日志 / 全部系统 ---

// ListAudit 分页查询操作日志（GET /admin/audit）。
func (c *Center) ListAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from, _ := strconv.ParseInt(q.Get("from"), 10, 64)
	to, _ := strconv.ParseInt(q.Get("to"), 10, 64)
	page, err := c.store.ListAudit(store.AuditFilter{
		Actor: strings.TrimSpace(q.Get("actor")), Action: strings.TrimSpace(q.Get("action")), From: from, To: to,
	}, parsePositiveInt(q.Get("page"), 1), parsePositiveInt(q.Get("pageSize"), 20))
	if err != nil {
		c.logger.Warn("Failed to list audit log", "err", err)
		writeError(w, http.StatusInternalServerError, "internal-error", "读取操作日志失败")
		return
	}
	writeJSON(w, http.StatusOK, page)
}
