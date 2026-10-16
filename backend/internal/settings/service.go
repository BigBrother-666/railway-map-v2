// Package settings 维护 web 端可编辑的服务器配置（原 config.yml 的 frontend / route 段）。
// 默认值在代码里（config.ApplyFrontendDefaults / ApplyRouteDefaults），数据库 app_settings 存整段覆盖值；
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

// Snapshot 是某一时刻生效的全部可编辑配置（不可变，替换整体）。
type Snapshot struct {
	Frontend     config.FrontendConfig
	Route        config.RouteConfig
	FrontendMeta Meta
	RouteMeta    Meta
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

	frontendRow, err := st.LoadSetting(SectionFrontend)
	if err != nil {
		return nil, err
	}
	switch {
	case frontendRow != nil:
		if err := json.Unmarshal(frontendRow.Payload, &snap.Frontend); err != nil {
			return nil, fmt.Errorf("解析数据库中的 frontend 配置失败: %w", err)
		}
		snap.FrontendMeta = Meta{UpdatedBy: frontendRow.UpdatedBy, UpdatedAt: frontendRow.UpdatedAt}
		if legacy.Frontend != nil {
			logger.Warn("config.yml 中的 frontend 段已废弃并被忽略，请在 web 端服务器配置中修改，并从 config.yml 删除该段")
		}
	case legacy.Frontend != nil:
		snap.Frontend = *legacy.Frontend
		config.ApplyFrontendDefaults(&snap.Frontend)
		if err := s.persist(SectionFrontend, snap.Frontend, "migration"); err != nil {
			return nil, err
		}
		snap.FrontendMeta = Meta{UpdatedBy: "migration"}
		logger.Info("已将 config.yml 中的 frontend 段导入数据库，可从 config.yml 删除该段")
	}
	config.ApplyFrontendDefaults(&snap.Frontend)

	routeRow, err := st.LoadSetting(SectionRoute)
	if err != nil {
		return nil, err
	}
	switch {
	case routeRow != nil:
		if err := json.Unmarshal(routeRow.Payload, &snap.Route); err != nil {
			return nil, fmt.Errorf("解析数据库中的 route 配置失败: %w", err)
		}
		snap.RouteMeta = Meta{UpdatedBy: routeRow.UpdatedBy, UpdatedAt: routeRow.UpdatedAt}
		if legacy.Route != nil {
			logger.Warn("config.yml 中的 route 段已废弃并被忽略，请在 web 端服务器配置中修改，并从 config.yml 删除该段")
		}
	case legacy.Route != nil:
		snap.Route = *legacy.Route
		config.ApplyRouteDefaults(&snap.Route)
		if err := s.persist(SectionRoute, snap.Route, "migration"); err != nil {
			return nil, err
		}
		snap.RouteMeta = Meta{UpdatedBy: "migration"}
		logger.Info("已将 config.yml 中的 route 段导入数据库，可从 config.yml 删除该段")
	}
	config.ApplyRouteDefaults(&snap.Route)

	s.cur.Store(snap)
	return s, nil
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

// Defaults 返回两段配置的代码默认值（供前端「恢复默认」与对比展示）。
func Defaults() (config.FrontendConfig, config.RouteConfig) {
	var f config.FrontendConfig
	var r config.RouteConfig
	config.ApplyFrontendDefaults(&f)
	config.ApplyRouteDefaults(&r)
	return f, r
}

// UpdateFrontend 校验并整段保存前端配置，成功后立即生效。
func (s *Service) UpdateFrontend(f config.FrontendConfig, actor string) error {
	if errs := ValidateFrontend(f); len(errs) > 0 {
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

// Reset 删除某段数据库记录，恢复代码默认值。
func (s *Service) Reset(section string) error {
	f, r := Defaults()
	switch section {
	case SectionFrontend:
		if err := s.store.DeleteSetting(section); err != nil {
			return err
		}
		s.swap(func(n *Snapshot) { n.Frontend = f; n.FrontendMeta = Meta{} })
	case SectionRoute:
		if err := s.store.DeleteSetting(section); err != nil {
			return err
		}
		s.swap(func(n *Snapshot) { n.Route = r; n.RouteMeta = Meta{} })
	default:
		return ErrUnknownSection
	}
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
