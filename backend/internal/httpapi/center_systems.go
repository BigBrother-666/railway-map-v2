package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"railway-map-backend/internal/model"
	"railway-map-backend/internal/pluginconfig"
	"railway-map-backend/internal/rbac"
)

// systemAccess 是当前玩家对某系统的权限。
type systemAccess struct {
	member    bool // 成员（含创建者）
	creator   bool
	manageAll bool
}

func (a systemAccess) canView() bool   { return a.member || a.manageAll }
func (a systemAccess) canEdit() bool   { return a.member || a.manageAll }
func (a systemAccess) canDelete() bool { return a.creator || a.manageAll }

// canEditMembers 创建者与管理员可修改成员（与游戏内 wizard 一致：成员列表步骤只对创建者显示）。
func (a systemAccess) canEditMembers() bool { return a.creator || a.manageAll }

func (c *Center) access(r *http.Request, sys model.PrivateSystem) systemAccess {
	uuid := playerOf(r).UUID
	return systemAccess{
		member:    sys.Creator == uuid || slices.Contains(sys.Members, uuid),
		creator:   sys.Creator == uuid,
		manageAll: hasPerm(r, rbac.PermSystemManage),
	}
}

// loadSystem 取系统并校验查看权限；失败时已写出响应并返回 false。
// 无权查看与不存在统一返回 404，避免探测他人系统。
func (c *Center) loadSystem(w http.ResponseWriter, r *http.Request, id string) (model.PrivateSystem, systemAccess, bool) {
	sys, ok := c.cache.PrivateSystem(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not-found", "铁路系统不存在")
		return sys, systemAccess{}, false
	}
	acc := c.access(r, sys)
	if !acc.canView() {
		writeError(w, http.StatusNotFound, "not-found", "铁路系统不存在")
		return sys, acc, false
	}
	return sys, acc, true
}

// systemView 是返回给前端的系统详情。
type systemView struct {
	model.PrivateSystem
	CreatorName string            `json:"creatorName"`
	MemberNames map[string]string `json:"memberNames"`
	LineCount   int               `json:"lineCount"`
	// EffectivePricePerKm 是实际生效的每公里票价（未单独配置时为插件全局默认值，取自公开系统快照）。
	EffectivePricePerKm *float64 `json:"effectivePricePerKm,omitempty"`
	CanEdit             bool     `json:"canEdit"`
	CanDelete           bool     `json:"canDelete"`
	CanMembers          bool     `json:"canEditMembers"`
}

func (c *Center) systemView(sys model.PrivateSystem, acc systemAccess) systemView {
	uuids := append([]string{sys.Creator}, sys.Members...)
	names, err := c.store.PlayerNames(uuids)
	if err != nil {
		names = map[string]string{}
	}
	lines := 0
	for _, l := range c.cache.PrivateLines() {
		if l.SystemID == sys.ID {
			lines++
		}
	}
	if sys.Members == nil {
		sys.Members = []string{}
	}
	return systemView{
		PrivateSystem: sys, CreatorName: names[sys.Creator], MemberNames: names, LineCount: lines,
		EffectivePricePerKm: c.effectivePrice(sys.ID),
		CanEdit:             acc.canEdit(), CanDelete: acc.canDelete(), CanMembers: acc.canEditMembers(),
	}
}

// effectivePrice 从公开系统快照取实际生效的每公里票价（插件对未配置的系统填全局默认值）。
func (c *Center) effectivePrice(systemID string) *float64 {
	var list []struct {
		ID         string   `json:"id"`
		PricePerKm *float64 `json:"pricePerKm"`
	}
	if b := c.cache.Systems(); b != nil && json.Unmarshal(b, &list) == nil {
		for _, s := range list {
			if s.ID == systemID {
				return s.PricePerKm
			}
		}
	}
	return nil
}

// GetSystem 返回系统详情（GET /me/systems/{id}）。
func (c *Center) GetSystem(w http.ResponseWriter, r *http.Request) {
	sys, acc, ok := c.loadSystem(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, c.systemView(sys, acc))
}

// systemUpdatePayload 是 system.update 下发给插件的负载。Members 为 nil 表示不修改成员。
type systemUpdatePayload struct {
	SystemID   string   `json:"systemId"`
	Name       string   `json:"name"`
	PricePerKm *float64 `json:"pricePerKm"`
	Members    []string `json:"members,omitempty"`
}

// UpdateSystem 修改系统名称 / 单价 / 成员（PUT /me/systems/{id}）。不能新建。
func (c *Center) UpdateSystem(w http.ResponseWriter, r *http.Request) {
	sys, acc, ok := c.loadSystem(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !acc.canEdit() {
		writeError(w, http.StatusForbidden, "forbidden", "没有权限编辑该系统")
		return
	}
	var req struct {
		Name       string    `json:"name"`
		PricePerKm *float64  `json:"pricePerKm"`
		Members    *[]string `json:"members"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeFieldError(w, "name", "系统名称不能为空")
		return
	}
	if req.PricePerKm != nil && *req.PricePerKm < 0 {
		writeFieldError(w, "pricePerKm", "单价不能为负数")
		return
	}
	payload := systemUpdatePayload{SystemID: sys.ID, Name: req.Name, PricePerKm: req.PricePerKm}
	if req.Members != nil {
		if !acc.canEditMembers() {
			writeError(w, http.StatusForbidden, "forbidden", "只有创建者可以修改成员")
			return
		}
		members := []string{}
		for _, m := range *req.Members {
			m = rbac.NormalizeUUID(m)
			if !uuidPattern.MatchString(m) {
				writeFieldError(w, "members", "成员 UUID 格式无效："+m)
				return
			}
			if !slices.Contains(members, m) {
				members = append(members, m)
			}
		}
		// 创建者不能被移出（与游戏内一致）
		if sys.Creator != "" && !slices.Contains(members, sys.Creator) {
			members = append([]string{sys.Creator}, members...)
		}
		payload.Members = members
	}
	detail := map[string]any{"before": sys, "after": payload}
	c.pluginCall(w, r, pluginconfig.OpSystemUpdate, "system.update", sys.ID, payload, detail)
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func writeFieldError(w http.ResponseWriter, field, msg string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{
		"error": model.APIErrorBody{Code: "invalid", Message: msg},
		"field": field,
	})
}

// DeleteSystem 删除系统及其全部线路（DELETE /me/systems/{id}），仅创建者或管理员。
func (c *Center) DeleteSystem(w http.ResponseWriter, r *http.Request) {
	sys, acc, ok := c.loadSystem(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !acc.canDelete() {
		writeError(w, http.StatusForbidden, "forbidden", "只有创建者可以删除铁路系统")
		return
	}
	var lineIDs []string
	for _, l := range c.cache.PrivateLines() {
		if l.SystemID == sys.ID {
			lineIDs = append(lineIDs, l.ID)
		}
	}
	c.pluginCall(w, r, pluginconfig.OpSystemDelete, "system.delete", sys.ID,
		map[string]string{"systemId": sys.ID},
		map[string]any{"before": sys, "lines": lineIDs})
}

// maxLogoBytes 与游戏内 wizard 一致：图片最大 3MB。
const maxLogoBytes = 3 << 20

var allowedLogoTypes = []string{"image/png", "image/jpeg", "image/webp", "image/gif"}

// UploadSystemLogo 上传系统 Logo（PUT /me/systems/{id}/logo/{kind}，multipart 字段 file）。
// kind = web | mc。后端校验大小与类型，插件负责解码、缩放与保存。
func (c *Center) UploadSystemLogo(w http.ResponseWriter, r *http.Request) {
	sys, acc, ok := c.loadSystem(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !acc.canEdit() {
		writeError(w, http.StatusForbidden, "forbidden", "没有权限编辑该系统")
		return
	}
	kind := chi.URLParam(r, "kind")
	if kind != "web" && kind != "mc" {
		writeError(w, http.StatusNotFound, "not-found", "未知的 Logo 类型")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoBytes+64<<10)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeFieldError(w, "file", "请选择不超过 3MB 的图片")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLogoBytes+1))
	if err != nil || len(data) == 0 {
		writeFieldError(w, "file", "读取图片失败")
		return
	}
	if len(data) > maxLogoBytes {
		writeFieldError(w, "file", "图片不能超过 3MB")
		return
	}
	if ct := http.DetectContentType(data); !slices.Contains(allowedLogoTypes, ct) {
		writeFieldError(w, "file", "只支持 PNG / JPEG / WebP / GIF 图片")
		return
	}
	c.pluginCall(w, r, pluginconfig.OpSystemLogo, "system.logo", sys.ID+"/"+kind,
		map[string]any{"systemId": sys.ID, "kind": kind, "imageBase64": base64.StdEncoding.EncodeToString(data)},
		map[string]any{"kind": kind, "bytes": len(data)})
}

// DeleteSystemLogo 清除系统 Logo（DELETE /me/systems/{id}/logo/{kind}），恢复默认图标。
func (c *Center) DeleteSystemLogo(w http.ResponseWriter, r *http.Request) {
	sys, acc, ok := c.loadSystem(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	if !acc.canEdit() {
		writeError(w, http.StatusForbidden, "forbidden", "没有权限编辑该系统")
		return
	}
	kind := chi.URLParam(r, "kind")
	if kind != "web" && kind != "mc" {
		writeError(w, http.StatusNotFound, "not-found", "未知的 Logo 类型")
		return
	}
	c.pluginCall(w, r, pluginconfig.OpSystemLogo, "system.logo", sys.ID+"/"+kind,
		map[string]any{"systemId": sys.ID, "kind": kind, "clear": true},
		map[string]any{"kind": kind, "clear": true})
}

// ListSystemLines 返回系统下全部线路完整配置（GET /me/systems/{id}/lines）。
func (c *Center) ListSystemLines(w http.ResponseWriter, r *http.Request) {
	sys, _, ok := c.loadSystem(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}
	lines := []model.PrivateLine{}
	for _, l := range c.cache.PrivateLines() {
		if l.SystemID == sys.ID {
			lines = append(lines, l)
		}
	}
	slices.SortFunc(lines, func(a, b model.PrivateLine) int { return strings.Compare(a.ID, b.ID) })
	writeJSON(w, http.StatusOK, lines)
}

// loadLine 取线路并校验其所属系统的编辑权限。
func (c *Center) loadLine(w http.ResponseWriter, r *http.Request) (model.PrivateLine, bool) {
	line, ok := c.cache.PrivateLine(chi.URLParam(r, "lineId"))
	if !ok {
		writeError(w, http.StatusNotFound, "not-found", "线路不存在")
		return line, false
	}
	sys, found := c.cache.PrivateSystem(line.SystemID)
	acc := systemAccess{manageAll: hasPerm(r, rbac.PermSystemManage)}
	if found {
		acc = c.access(r, sys)
	}
	if !acc.canEdit() {
		writeError(w, http.StatusNotFound, "not-found", "线路不存在")
		return line, false
	}
	return line, true
}

var (
	hexColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	bossbarColors   = []string{"PINK", "BLUE", "RED", "GREEN", "YELLOW", "PURPLE", "WHITE"}
)

// UpdateLine 修改线路配置（PUT /me/lines/{lineId}）。不能新建；改所属系统时需对目标系统也有编辑权限。
// 字段格式在这里做基础校验，完整规则（站点格式、提示音格式等）由插件复用游戏内 wizard 校验。
func (c *Center) UpdateLine(w http.ResponseWriter, r *http.Request) {
	line, ok := c.loadLine(w, r)
	if !ok {
		return
	}
	var req model.PrivateLine
	if !decodeJSON(w, r, &req) {
		return
	}
	req.ID = line.ID
	req.Name = strings.TrimSpace(req.Name)
	req.SystemID = strings.TrimSpace(req.SystemID)
	switch {
	case req.Name == "":
		writeFieldError(w, "name", "线路名称不能为空")
		return
	case !hexColorPattern.MatchString(req.Color):
		writeFieldError(w, "color", "颜色必须是 #RRGGBB 格式")
		return
	case len(req.BossbarStations) == 0:
		writeFieldError(w, "bossbarStations", "车站列表不能为空")
		return
	case req.BossbarColor != "" && !slices.Contains(bossbarColors, req.BossbarColor):
		writeFieldError(w, "bossbarColor", "BossBar 颜色无效")
		return
	}
	if field, msg := validateTransfer(line.ID, req.BossbarStations, c.cache.PrivateLines()); msg != "" {
		writeFieldError(w, field, msg)
		return
	}
	if req.SystemID != line.SystemID {
		target, found := c.cache.PrivateSystem(req.SystemID)
		if !found || !c.access(r, target).canEdit() {
			writeFieldError(w, "systemId", "目标铁路系统不存在或没有权限")
			return
		}
	}
	c.pluginCall(w, r, pluginconfig.OpLineUpdate, "line.update", line.ID, req,
		map[string]any{"before": line, "after": req})
}

// contactID 是联络线的特殊线路 / 系统 id，不能作为转线目标。
const contactID = "contact"

// splitTransfer 识别 bossbar-stations 末项是否为转线目标（"<线路id>" 或 "<线路id>:<进入站>"，与插件 LineConfig 规则一致），
// 返回目标线路、进入站与去掉末项后的车站列表。
func splitTransfer(selfID string, items []string, lines []model.PrivateLine) (target *model.PrivateLine, entry string, stations []string) {
	if len(items) == 0 {
		return nil, "", items
	}
	last := strings.TrimSpace(items[len(items)-1])
	candidate, rest, _ := strings.Cut(last, ":")
	candidate = strings.TrimSpace(candidate)
	if candidate == "" || candidate == selfID {
		return nil, "", items
	}
	for i := range lines {
		if lines[i].ID == candidate {
			return &lines[i], strings.TrimSpace(rest), items[:len(items)-1]
		}
	}
	return nil, "", items
}

// stationNames 返回线路的车站名（去掉折返标记 :RV 与末项转线目标）。
func stationNames(l model.PrivateLine, lines []model.PrivateLine) []string {
	_, _, items := splitTransfer(l.ID, l.BossbarStations, lines)
	names := make([]string, 0, len(items))
	for _, it := range items {
		if i := strings.LastIndex(it, ":"); i >= 0 && strings.EqualFold(strings.TrimSpace(it[i+1:]), "RV") {
			it = it[:i]
		}
		names = append(names, strings.TrimSpace(it))
	}
	return names
}

// validateTransfer 校验转线：目标不能是联络线，进入站必填且必须是目标线路的车站。
func validateTransfer(selfID string, items []string, lines []model.PrivateLine) (field, msg string) {
	target, entry, _ := splitTransfer(selfID, items, lines)
	switch {
	case target == nil:
		return "", ""
	case target.ID == contactID || target.SystemID == contactID:
		return "nextLineId", "不能转入联络线"
	case entry == "":
		return "nextEntryStation", "请选择转线后的进入站"
	case !slices.Contains(stationNames(*target, lines), entry):
		return "nextEntryStation", "进入站不是转入线路的车站"
	}
	return "", ""
}

// DeleteLine 删除线路（DELETE /me/lines/{lineId}）。
func (c *Center) DeleteLine(w http.ResponseWriter, r *http.Request) {
	line, ok := c.loadLine(w, r)
	if !ok {
		return
	}
	c.pluginCall(w, r, pluginconfig.OpLineDelete, "line.delete", line.ID,
		map[string]string{"lineId": line.ID}, map[string]any{"before": line})
}

// SearchPlayers 搜索已知玩家（GET /me/players?q=），供成员编辑 / 授权使用。
func (c *Center) SearchPlayers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	players, err := c.store.SearchPlayers(q, 20)
	if err != nil {
		c.logger.Warn("Player search failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal-error", "搜索失败")
		return
	}
	writeJSON(w, http.StatusOK, players)
}
