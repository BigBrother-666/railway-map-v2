package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"railway-map-backend/internal/model"
	"railway-map-backend/internal/pluginconfig"
)

// 车站别名 / 车站组是全服共享数据（插件 station_alias.yml / station_group.yml），
// 读取用公开快照 snapshot.stations，写入转发插件执行；规则与游戏内 /ticketconfig 指令一致。

func (c *Center) stationsSnapshot() model.SnapshotStationsData {
	var d model.SnapshotStationsData
	if b, _ := c.cache.Stations(); b != nil {
		_ = json.Unmarshal(b, &d)
	}
	if d.Aliases == nil {
		d.Aliases = map[string]string{}
	}
	if d.Groups == nil {
		d.Groups = map[string][]string{}
	}
	return d
}

// ListStationAliases 返回别名表与全部车站名（GET /me/station-aliases）。
func (c *Center) ListStationAliases(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"aliases":  c.stationsSnapshot().Aliases,
		"stations": c.cache.StationNames(),
	})
}

// UpsertStationAlias 新增 / 修改车站英文名（PUT /me/station-aliases/{station}）。
func (c *Center) UpsertStationAlias(w http.ResponseWriter, r *http.Request) {
	station := strings.TrimSpace(chi.URLParam(r, "station"))
	var req struct {
		EnglishName string `json:"englishName"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	// 与游戏内一致：合并连续空白，保留单词间单个空格
	en := strings.Join(strings.Fields(req.EnglishName), " ")
	if station == "" {
		writeFieldError(w, "station", "车站名不能为空")
		return
	}
	if en == "" {
		writeFieldError(w, "englishName", "英文车站名不能为空")
		return
	}
	old, existed := c.stationsSnapshot().Aliases[station]
	detail := map[string]any{"station": station, "after": en}
	if existed {
		detail["before"] = old
	}
	c.pluginCall(w, r, pluginconfig.OpStationAliasUpsert, "station_alias.upsert", station,
		map[string]string{"station": station, "englishName": en}, detail)
}

// DeleteStationAlias 删除车站英文名（DELETE /me/station-aliases/{station}）。
func (c *Center) DeleteStationAlias(w http.ResponseWriter, r *http.Request) {
	station := strings.TrimSpace(chi.URLParam(r, "station"))
	old, existed := c.stationsSnapshot().Aliases[station]
	if !existed {
		writeError(w, http.StatusNotFound, "not-found", "该车站没有设置英文名")
		return
	}
	c.pluginCall(w, r, pluginconfig.OpStationAliasDelete, "station_alias.delete", station,
		map[string]string{"station": station}, map[string]any{"station": station, "before": old})
}

// ListStationGroups 返回车站组与全部车站名（GET /me/station-groups）。
func (c *Center) ListStationGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"groups":   c.stationsSnapshot().Groups,
		"stations": c.cache.StationNames(),
	})
}

// UpsertStationGroup 新建 / 覆盖车站组（PUT /me/station-groups/{group}）。
// 至少两个不同车站；一个车站只能属于一个组（覆盖本组时本组原有车站不算冲突）。
func (c *Center) UpsertStationGroup(w http.ResponseWriter, r *http.Request) {
	group := strings.TrimSpace(chi.URLParam(r, "group"))
	var req struct {
		Stations []string `json:"stations"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if group == "" || strings.ContainsAny(group, " \t") {
		writeFieldError(w, "group", "组名不能为空且不能包含空格")
		return
	}
	stations := []string{}
	for _, s := range req.Stations {
		s = strings.TrimSpace(s)
		if s != "" && !slices.Contains(stations, s) {
			stations = append(stations, s)
		}
	}
	if len(stations) < 2 {
		writeFieldError(w, "stations", "车站组至少需要两个不同的车站")
		return
	}
	snap := c.stationsSnapshot()
	conflicts := map[string]string{}
	for name, members := range snap.Groups {
		if name == group {
			continue
		}
		for _, s := range stations {
			if slices.Contains(members, s) {
				conflicts[s] = name
			}
		}
	}
	if len(conflicts) > 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":     model.APIErrorBody{Code: "conflict", Message: "部分车站已属于其它车站组，一个车站只能属于一个组"},
			"field":     "stations",
			"conflicts": conflicts,
		})
		return
	}
	detail := map[string]any{"group": group, "after": stations}
	if old, ok := snap.Groups[group]; ok {
		detail["before"] = old
	}
	c.pluginCall(w, r, pluginconfig.OpStationGroupUpsert, "station_group.upsert", group,
		map[string]any{"group": group, "stations": stations}, detail)
}

// DeleteStationGroup 删除车站组（DELETE /me/station-groups/{group}）。
func (c *Center) DeleteStationGroup(w http.ResponseWriter, r *http.Request) {
	group := strings.TrimSpace(chi.URLParam(r, "group"))
	old, ok := c.stationsSnapshot().Groups[group]
	if !ok {
		writeError(w, http.StatusNotFound, "not-found", "车站组不存在")
		return
	}
	c.pluginCall(w, r, pluginconfig.OpStationGroupDelete, "station_group.delete", group,
		map[string]string{"group": group}, map[string]any{"group": group, "before": old})
}
