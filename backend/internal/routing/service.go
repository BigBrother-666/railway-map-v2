package routing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/model"
)

// GeoSource 是 Service 依赖的最小 geo 数据源接口，geo.Cache 已满足。
type GeoSource interface {
	Geojson() ([]byte, string)
	Lines() []byte
	Systems() []byte
}

// Service 持有懒重建的寻路图：geo 版本变化时重建，用互斥锁防并发重复建图。
// 同时按 (起点站名, 终点站名) 缓存查询结果（空间换时间）：同一份地图数据下，某些站点对的
// K-最短路计算可能很贵（例如两站间根本无合法路线，kShortest 要扩展到安全阀才放弃），缓存后
// 同一站点对的重复查询直接命中，不必每次重算。地图数据更新（geo 版本变化）时随图一起整体换新。
type Service struct {
	source GeoSource
	cfg    func() config.RouteConfig

	mu          sync.RWMutex
	graph       *Graph
	graphVer    string
	reverseSet  map[string]struct{}
	systems     map[string]System
	globalPrice float64

	// resultCache 缓存 (startStation, endStation) → []model.RoutePath，随图版本整体替换。
	resultCache atomic.Pointer[sync.Map]
	// inflight 折叠同一时刻对同一站点对的并发重复计算，避免多个请求各自跑一遍昂贵的寻路。
	inflight singleflight.Group
}

// NewService 创建路线查询服务。cfgFn 每次查询都会调用一次，以便配置热更新（若未来支持）立即生效；
// 目前配置在启动时加载一次，cfgFn 可简单地返回同一份 config.RouteConfig。
func NewService(source GeoSource, cfgFn func() config.RouteConfig, globalPricePerKm float64) *Service {
	s := &Service{source: source, cfg: cfgFn, globalPrice: globalPricePerKm}
	s.resultCache.Store(&sync.Map{})
	return s
}

type lineProps struct {
	ID              string   `json:"id"`
	ReverseStations []string `json:"reverseStations,omitempty"`
}

type systemProps struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	PricePerKm *float64 `json:"pricePerKm"`
}

// graphSnapshot 是与某个 geo 版本绑定的图 + 派生数据 + 结果缓存的一致快照。
type graphSnapshot struct {
	version    string
	graph      *Graph
	reverseSet map[string]struct{}
	systems    map[string]System
	cache      *sync.Map // (version+startStation+endStation) → []model.RoutePath
}

// ensureGraph 确保图与派生数据（折返站集合、系统票价表）与当前 geo 版本一致；返回是否有可用数据。
func (s *Service) ensureGraph() (*graphSnapshot, bool, error) {
	payload, version := s.source.Geojson()
	if payload == nil {
		return nil, false, nil
	}

	s.mu.RLock()
	if s.graph != nil && s.graphVer == version {
		snap := s.snapshotLocked(version)
		s.mu.RUnlock()
		return snap, true, nil
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	// 双重检查：等锁期间可能已被其它请求重建
	if s.graph != nil && s.graphVer == version {
		return s.snapshotLocked(version), true, nil
	}

	var fc model.FeatureCollection
	if err := json.Unmarshal(payload, &fc); err != nil {
		return nil, false, fmt.Errorf("解析 geojson 失败: %w", err)
	}
	g, err := BuildFromGeoJSON(&fc)
	if err != nil {
		return nil, false, err
	}

	reverseSet := make(map[string]struct{})
	var lines []lineProps
	if b := s.source.Lines(); len(b) > 0 {
		_ = json.Unmarshal(b, &lines)
	}
	for _, l := range lines {
		for _, st := range l.ReverseStations {
			reverseSet[l.ID+"|"+st] = struct{}{}
		}
	}

	systems := make(map[string]System)
	var sysList []systemProps
	if b := s.source.Systems(); len(b) > 0 {
		_ = json.Unmarshal(b, &sysList)
	}
	for _, sp := range sysList {
		sys := System{ID: sp.ID, Name: sp.Name}
		if sp.PricePerKm != nil {
			sys.PricePerKm = *sp.PricePerKm
			sys.HasPrice = true
		}
		systems[sp.ID] = sys
	}

	s.graph = g
	s.graphVer = version
	s.reverseSet = reverseSet
	s.systems = systems
	s.resultCache.Store(&sync.Map{}) // 地图数据变了，旧站点对结果全部作废
	return s.snapshotLocked(version), true, nil
}

// snapshotLocked 在持有 s.mu（读或写锁均可）期间打包当前图状态。调用方必须已确认 s.graphVer == version。
func (s *Service) snapshotLocked(version string) *graphSnapshot {
	return &graphSnapshot{
		version:    version,
		graph:      s.graph,
		reverseSet: s.reverseSet,
		systems:    s.systems,
		cache:      s.resultCache.Load(),
	}
}

// ErrNoData 表示 geo 数据尚未就绪（插件从未推送过 geojson）。
var ErrNoData = fmt.Errorf("geojson 尚未就绪")

// ErrComputeTimeout 表示单次寻路计算超过 config.RouteConfig.ComputeTimeoutMs 仍未算出任何结果
// （空结果 + 超时的组合，见 Query 内部说明）。此时结果未被缓存，可以直接重试。
var ErrComputeTimeout = errors.New("路线计算超时")

// testComputeHook 是仅供测试用的同步点：在每次实际计算（singleflight 未命中缓存时）前调用。
// 生产环境恒为 nil，不影响正常路径。
var testComputeHook func()

// testComputeDoneHook 是仅供测试用的同步点：在 ComputeCandidates 真正返回之后调用（区别于
// testComputeHook 在计算开始前调用）。生产环境恒为 nil。用于测试可靠地等待"调用者已放弃、但后台
// 计算仍在独立跑"这一 goroutine 真正执行完，而不是只等到计算刚开始时的那个同步点。
var testComputeDoneHook func()

// Query 按起终点站名查询候选路线（直达 + 联程票），已附带票价明细，按配置排序。结果按
// (geo 版本, 起点站名, 终点站名) 缓存：命中直接返回；未命中用 singleflight 折叠并发重复计算，
// 计算体内部用独立于调用者的 context.Background() 跑到底并写入缓存（不因某个调用者取消而中断，
// 让"注定要花的计算成本"全服务生命周期只花一次），但调用者本身仍按自己的 ctx 决定等多久——
// ctx 被取消（超时或被同玩家下一次查询取代）时立刻返回 ctx.Err()，不会陪着后台那份计算傻等。
func (s *Service) Query(ctx context.Context, startStation, endStation string) ([]model.RoutePath, error) {
	snap, ok, err := s.ensureGraph()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoData
	}

	key := startStation + "\x00" + endStation
	if v, found := snap.cache.Load(key); found {
		return v.([]model.RoutePath), nil
	}

	isReverse := func(lineID, stationName string) bool {
		_, ok := snap.reverseSet[lineID+"|"+stationName]
		return ok
	}
	cfg := s.cfg()
	// singleflight key 带版本号：避免地图刚更新的极短窗口内，旧版本的进行中计算与新版本的
	// 新请求撞上同一个站点对，被误判成同一份计算而共享结果（缓存本身已按版本隔离，这里额外保护
	// 的是"合并计算"这一步，而不是"读写缓存"这一步）。
	inflightKey := snap.version + "\x00" + key
	resultCh := s.inflight.DoChan(inflightKey, func() (any, error) {
		// 计算体用独立于任何调用者的 context——它可能同时被多个玩家的请求共享，不能被某一个
		// 调用者的放弃/超时打断；但要给它自己的耗时上限，防止极端查询无限占用服务器 CPU。
		computeCtx := context.Background()
		if cfg.ComputeTimeoutMs > 0 {
			var cancel context.CancelFunc
			computeCtx, cancel = context.WithTimeout(computeCtx, time.Duration(cfg.ComputeTimeoutMs)*time.Millisecond)
			defer cancel()
		}
		if testComputeHook != nil {
			testComputeHook() // 测试专用同步点，生产环境恒为 nil
		}
		input := ComputeInput{
			StartStation:     startStation,
			EndStation:       endStation,
			Systems:          snap.systems,
			IsReverse:        isReverse,
			Cfg:              cfg,
			GlobalPricePerKm: s.globalPrice,
		}
		result := ComputeCandidates(computeCtx, snap.graph, input)
		if testComputeDoneHook != nil {
			testComputeDoneHook() // 测试专用同步点：计算真正结束后调用，生产环境恒为 nil
		}
		if len(result) == 0 && computeCtx.Err() != nil {
			// 超时且什么都没算出来：无法区分「两站真的无路线」和「还没来得及判断出结果」，
			// 不能缓存这个有歧义的空结果。
			return nil, ErrComputeTimeout
		}
		// 其余情况都缓存并当正常结果返回，即使 computeCtx 已超时：kShortest/FindTransferJourneys
		// 里非空结果的每一条都是完整实体化的真实路径（不存在"半条路径"），只是数量可能没达到 K 或
		// 候选上限——这本来就是正常情况下（撞 maxPops/materializeCap）也会发生的事，缓存它好过
		// 「这次白算一场、下次同样超时、永远拿不到任何结果」。
		snap.cache.Store(key, result)
		return result, nil
	})

	select {
	case res := <-resultCh:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.([]model.RoutePath), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
