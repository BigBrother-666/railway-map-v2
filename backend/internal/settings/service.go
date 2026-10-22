// Package settings 维护 web 端可编辑的服务器配置（原 config.yml 的 frontend / route / plugin（除 sharedToken）/ realtime 段，
// 以及 tiles 世界底图段（静态瓦片 + 外部地图来源，原 frontend.worldTiles））。
// 默认值在代码里（config.Apply*Defaults），数据库 app_settings 存整段覆盖值；
// 不与 config.yml 合并。保存后通过原子快照立即对后续请求生效。
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/store"
)

// 配置段名（app_settings.section）。
const (
	SectionFrontend = "frontend"
	SectionRoute    = "route"
	SectionPlugin   = "plugin"
	SectionRealtime = "realtime"
	SectionTiles    = "tiles"
)

// ErrUnknownSection 表示请求了不存在的配置段。
var ErrUnknownSection = errors.New("unknown settings section")

// Store 是配置持久化接口（由 store.Store 实现）。
type Store interface {
	LoadSetting(section string) (*store.Setting, error)
	SaveSetting(section string, payload []byte, updatedBy string) error
	DeleteSetting(section string) error
}

// Meta 是某段配置的最后修改信息（未修改过时为零值）。
type Meta struct {
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
}

// Values 是全部可编辑配置段的取值。
type Values struct {
	Frontend config.FrontendConfig `json:"frontend"`
	Route    config.RouteConfig    `json:"route"`
	Plugin   config.PluginSettings `json:"plugin"`
	Realtime config.RealtimeConfig `json:"realtime"`
	Tiles    config.TilesConfig    `json:"tiles"`
}

// Snapshot 是某一时刻生效的全部可编辑配置（不可变，替换整体）。
type Snapshot struct {
	Values
	FrontendMeta Meta
	RouteMeta    Meta
	PluginMeta   Meta
	RealtimeMeta Meta
	TilesMeta    Meta
}

// Service 持有当前生效配置。
type Service struct {
	store  Store
	logger *slog.Logger
	cur    atomic.Pointer[Snapshot]
}

// New 从数据库加载配置；数据库中某段不存在而旧 config.yml 中仍有该段时，一次性导入数据库。
func New(st Store, legacy config.LegacySections, logger *slog.Logger) (*Service, error) {
	s := &Service{store: st, logger: logger}
	snap := &Snapshot{}
	var err error
	if snap.FrontendMeta, err = load(s, SectionFrontend, legacy.Frontend, &snap.Frontend, config.ApplyFrontendDefaults); err != nil {
		return nil, err
	}
	if snap.RouteMeta, err = load(s, SectionRoute, legacy.Route, &snap.Route, config.ApplyRouteDefaults); err != nil {
		return nil, err
	}
	if snap.PluginMeta, err = load(s, SectionPlugin, legacy.Plugin, &snap.Plugin, config.ApplyPluginDefaults); err != nil {
		return nil, err
	}
	if snap.RealtimeMeta, err = load(s, SectionRealtime, legacy.Realtime, &snap.Realtime, config.ApplyRealtimeDefaults); err != nil {
		return nil, err
	}
	if err := s.migrateWorldTiles(); err != nil {
		return nil, err
	}
	legacyTiles := legacy.Tiles
	if row, err := st.LoadSetting(SectionTiles); err != nil {
		return nil, err
	} else if row != nil {
		legacyTiles = nil // 已有 tiles 段；config.yml 的 frontend 段已由上面的 frontend 加载提示废弃
	}
	if snap.TilesMeta, err = load(s, SectionTiles, legacyTiles, &snap.Tiles, config.ApplyTilesDefaults); err != nil {
		return nil, err
	}
	s.cur.Store(snap)
	return s, nil
}

// migrateWorldTiles 把旧版数据库 frontend 段中的 worldTiles 移到 tiles 段（仅 tiles 段尚不存在时）。
// frontend 段中残留的 worldTiles 键不再被读取，下次保存前端配置时会被覆盖掉。
func (s *Service) migrateWorldTiles() error {
	if row, err := s.store.LoadSetting(SectionTiles); err != nil || row != nil {
		return err
	}
	row, err := s.store.LoadSetting(SectionFrontend)
	if err != nil || row == nil {
		return err
	}
	var old struct {
		WorldTiles map[string]config.LegacyWorldTileConfig `json:"worldTiles"`
	}
	if err := json.Unmarshal(row.Payload, &old); err != nil {
		return fmt.Errorf("解析数据库中的 %s 配置失败: %w", SectionFrontend, err)
	}
	if old.WorldTiles == nil {
		return nil
	}
	t := config.TilesConfig{WorldTiles: old.WorldTiles}
	config.ApplyTilesDefaults(&t)
	if err := s.persist(SectionTiles, t, "migration"); err != nil {
		return err
	}
	s.logger.Info("已将前端配置中的世界底图（worldTiles）移到世界底图配置段")
	return nil
}

// load 读取一段配置到 dst：数据库优先；数据库没有而 legacy 非 nil 时导入数据库；最后填充默认值。
func load[T any](s *Service, section string, legacy *T, dst *T, applyDefaults func(*T)) (Meta, error) {
	var meta Meta
	row, err := s.store.LoadSetting(section)
	if err != nil {
		return meta, err
	}
	switch {
	case row != nil:
		if err := json.Unmarshal(row.Payload, dst); err != nil {
			return meta, fmt.Errorf("解析数据库中的 %s 配置失败: %w", section, err)
		}
		meta = Meta{UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt}
		if legacy != nil {
			s.logger.Warn(fmt.Sprintf("config.yml 中的 %s 段已废弃并被忽略，请在 web 端服务器配置中修改，并从 config.yml 删除该段", section))
		}
	case legacy != nil:
		*dst = *legacy
		applyDefaults(dst)
		if err := s.persist(section, *dst, "migration"); err != nil {
			return meta, err
		}
		meta = Meta{UpdatedBy: "migration"}
		s.logger.Info(fmt.Sprintf("已将 config.yml 中的 %s 段导入数据库，可从 config.yml 删除该段", section))
	}
	applyDefaults(dst)
	return meta, nil
}

// Current 返回当前生效配置快照（只读）。
func (s *Service) Current() *Snapshot {
	return s.cur.Load()
}

// Frontend 返回当前前端运行时配置。
func (s *Service) Frontend() config.FrontendConfig {
	return s.cur.Load().Frontend
}

// Route 返回当前路线查询参数。
func (s *Service) Route() config.RouteConfig {
	return s.cur.Load().Route
}

// Plugin 返回当前插件链路参数。
func (s *Service) Plugin() config.PluginSettings {
	return s.cur.Load().Plugin
}

// Realtime 返回当前实时列车参数。
func (s *Service) Realtime() config.RealtimeConfig {
	return s.cur.Load().Realtime
}

// Tiles 返回当前世界底图配置。
func (s *Service) Tiles() config.TilesConfig {
	return s.cur.Load().Tiles
}

// Defaults 返回各段配置的代码默认值（供前端「恢复默认」与对比展示）。
func Defaults() Values {
	var v Values
	config.ApplyFrontendDefaults(&v.Frontend)
	config.ApplyRouteDefaults(&v.Route)
	config.ApplyPluginDefaults(&v.Plugin)
	config.ApplyRealtimeDefaults(&v.Realtime)
	config.ApplyTilesDefaults(&v.Tiles)
	return v
}

// UpdateFrontend 校验并整段保存前端配置，成功后立即生效。
func (s *Service) UpdateFrontend(f config.FrontendConfig, actor string) error {
	if errs := ValidateFrontend(f, s.Tiles()); len(errs) > 0 {
		return errs
	}
	config.ApplyFrontendDefaults(&f)
	if err := s.persist(SectionFrontend, f, actor); err != nil {
		return err
	}
	s.swap(func(n *Snapshot) { n.Frontend = f; n.FrontendMeta = s.meta(SectionFrontend) })
	return nil
}

// UpdateRoute 校验并整段保存路线查询参数，成功后立即生效。
func (s *Service) UpdateRoute(r config.RouteConfig, actor string) error {
	if errs := ValidateRoute(r); len(errs) > 0 {
		return errs
	}
	config.ApplyRouteDefaults(&r)
	if err := s.persist(SectionRoute, r, actor); err != nil {
		return err
	}
	s.swap(func(n *Snapshot) { n.Route = r; n.RouteMeta = s.meta(SectionRoute) })
	return nil
}

// UpdatePlugin 校验并整段保存插件链路参数，成功后立即生效。
func (s *Service) UpdatePlugin(p config.PluginSettings, actor string) error {
	if errs := ValidatePlugin(p); len(errs) > 0 {
		return errs
	}
	config.ApplyPluginDefaults(&p)
	if err := s.persist(SectionPlugin, p, actor); err != nil {
		return err
	}
	s.swap(func(n *Snapshot) { n.Plugin = p; n.PluginMeta = s.meta(SectionPlugin) })
	return nil
}

// UpdateRealtime 校验并整段保存实时列车参数，成功后立即生效（发送缓冲只影响新连接）。
func (s *Service) UpdateRealtime(r config.RealtimeConfig, actor string) error {
	if errs := ValidateRealtime(r); len(errs) > 0 {
		return errs
	}
	config.ApplyRealtimeDefaults(&r)
	if err := s.persist(SectionRealtime, r, actor); err != nil {
		return err
	}
	s.swap(func(n *Snapshot) { n.Realtime = r; n.RealtimeMeta = s.meta(SectionRealtime) })
	return nil
}

// UpdateTiles 校验并整段保存世界底图配置，成功后立即生效（来源变化由 tilesource.Service 重新发现）。
func (s *Service) UpdateTiles(t config.TilesConfig, actor string) error {
	if errs := ValidateTiles(t, s.Frontend().DefaultWorld); len(errs) > 0 {
		return errs
	}
	config.ApplyTilesDefaults(&t)
	if err := s.persist(SectionTiles, t, actor); err != nil {
		return err
	}
	s.swap(func(n *Snapshot) { n.Tiles = t; n.TilesMeta = s.meta(SectionTiles) })
	return nil
}

// Reset 删除某段数据库记录，恢复代码默认值。
func (s *Service) Reset(section string) error {
	d := Defaults()
	var mut func(*Snapshot)
	switch section {
	case SectionFrontend:
		mut = func(n *Snapshot) { n.Frontend = d.Frontend; n.FrontendMeta = Meta{} }
	case SectionRoute:
		mut = func(n *Snapshot) { n.Route = d.Route; n.RouteMeta = Meta{} }
	case SectionPlugin:
		mut = func(n *Snapshot) { n.Plugin = d.Plugin; n.PluginMeta = Meta{} }
	case SectionRealtime:
		mut = func(n *Snapshot) { n.Realtime = d.Realtime; n.RealtimeMeta = Meta{} }
	case SectionTiles:
		mut = func(n *Snapshot) { n.Tiles = d.Tiles; n.TilesMeta = Meta{} }
	default:
		return ErrUnknownSection
	}
	if err := s.store.DeleteSetting(section); err != nil {
		return err
	}
	s.swap(mut)
	return nil
}

func (s *Service) persist(section string, v any, actor string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.store.SaveSetting(section, b, actor)
}

func (s *Service) meta(section string) Meta {
	row, err := s.store.LoadSetting(section)
	if err != nil || row == nil {
		return Meta{}
	}
	return Meta{UpdatedBy: row.UpdatedBy, UpdatedAt: row.UpdatedAt}
}

// swap 复制当前快照、修改后原子替换（写操作很少，复制成本可忽略）。
func (s *Service) swap(mut func(*Snapshot)) {
	next := *s.cur.Load()
	mut(&next)
	s.cur.Store(&next)
}
