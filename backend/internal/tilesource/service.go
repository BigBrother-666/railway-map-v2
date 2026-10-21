package tilesource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/geo"
)

// ProxyPath 是瓦片代理路由前缀；完整路径为 ProxyPath/{source}/{world}/{z}/{x}/{y}。
const ProxyPath = "/api/v1/tiles"

const (
	maxMetadataBytes = 8 << 20
	maxTileBytes     = 4 << 20
	maxZoomLevel     = 30
)

// transparentPNG 是 1×1 透明 PNG：上游没有该瓦片（未渲染区域）时返回，避免前端刷 404。
var transparentPNG = func() []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1)))
	return buf.Bytes()
}()

// ErrNotFound 表示请求的来源 / 世界 / 瓦片不存在。
var ErrNotFound = errors.New("tile not found")

// Service 维护各来源的发现结果，并代理 + 缓存瓦片。配置通过 getter 读取，web 端修改后由 Reload 生效。
type Service struct {
	settings func() config.TilesConfig
	// worldBounds 返回各世界线路 / 车站坐标范围，限定合成层级只覆盖有数据的区域（nil 时不限制）。
	worldBounds func() map[string]geo.Bounds
	client      *http.Client
	logger      *slog.Logger

	state  atomic.Pointer[state]
	cache  *tileCache
	flight singleflight.Group
	reload chan struct{}

	semMu sync.Mutex
	sem   chan struct{}
}

// state 是某次发现的结果（不可变，整体替换）。
type state struct {
	key     string // 来源列表的指纹，变化时重新发现并清空瓦片缓存
	sources []*sourceState
}

type sourceState struct {
	cfg          config.TileSourceConfig
	worlds       []World
	err          string
	discoveredAt time.Time
}

// New 创建服务；调用 Run 后开始后台发现。worldBounds 可为 nil。
func New(settings func() config.TilesConfig, worldBounds func() map[string]geo.Bounds, logger *slog.Logger) *Service {
	s := &Service{
		settings:    settings,
		worldBounds: worldBounds,
		client:      &http.Client{},
		logger:      logger,
		cache:       newTileCache(),
		reload:      make(chan struct{}, 1),
	}
	s.state.Store(&state{})
	return s
}

// Run 立即发现一次，之后按 discoveryRefreshSeconds 周期刷新，或在 Reload 时立即刷新；ctx 取消后退出。
func (s *Service) Run(ctx context.Context) {
	for {
		s.refresh(ctx)
		interval := time.Duration(s.settings().DiscoveryRefreshSeconds) * time.Second
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		case <-s.reload:
		}
	}
}

// Reload 请求立即重新发现（服务器配置保存后调用；非阻塞）。
func (s *Service) Reload() {
	select {
	case s.reload <- struct{}{}:
	default:
	}
}

func sourcesKey(srcs []config.TileSourceConfig) string {
	b, _ := json.Marshal(srcs)
	return string(b)
}

// refresh 重新发现全部来源；某来源失败时沿用其上一次成功的结果（来源配置未变时）。
func (s *Service) refresh(ctx context.Context) {
	cfg := s.settings()
	prev := s.state.Load()
	key := sourcesKey(cfg.Sources)
	next := &state{key: key}
	for i, src := range cfg.Sources {
		ss := &sourceState{cfg: src}
		p, err := newProvider(src)
		if err == nil {
			ss.worlds, err = p.Discover(ctx, s.fetchMetadata)
		}
		if err != nil {
			ss.err = err.Error()
			if prev.key == key && prev.sources[i].worlds != nil {
				ss.worlds, ss.discoveredAt = prev.sources[i].worlds, prev.sources[i].discoveredAt
			}
			s.logger.Warn("Tile source discovery failed", "source", src.ID, "url", src.URL, "err", err)
		} else {
			ss.discoveredAt = time.Now()
			s.logger.Info("Tile source discovered", "source", src.ID, "worlds", availableWorlds(ss.worlds))
		}
		next.sources = append(next.sources, ss)
	}
	if prev.key != key {
		s.cache.clear()
	}
	s.state.Store(next)
}

func availableWorlds(ws []World) []string {
	var names []string
	for _, w := range ws {
		if w.Layer != nil {
			names = append(names, w.Name+"/"+w.Map)
		}
	}
	return names
}

func (s *Service) fetchMetadata(ctx context.Context, rawURL string) ([]byte, error) {
	body, _, status, err := s.get(ctx, rawURL, maxMetadataBytes)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", rawURL, status)
	}
	return body, nil
}

// get 以 GET 读取上游地址（超时读当前配置），返回 body、Content-Type 与状态码。
func (s *Service) get(ctx context.Context, rawURL string, limit int64) ([]byte, string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.settings().UpstreamTimeoutSeconds)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("User-Agent", "railway-map-backend")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", 0, err
	}
	if int64(len(body)) > limit {
		return nil, "", 0, fmt.Errorf("GET %s: 响应超过 %d 字节", rawURL, limit)
	}
	return body, resp.Header.Get("Content-Type"), resp.StatusCode, nil
}

// WorldTiles 返回下发前端的世界底图：以配置中的 worldTiles 为基础，外部来源提供的世界优先使用外部地图
// （同一世界取第一个来源），只沿用静态配置中的视图参数 zoom / center / opacity / minZoom / maxZoom
// （minZoom 不低于有底图的推导下限，否则缩小后底图消失）；外部来源没有的世界使用静态瓦片。
// 返回新 map，不修改配置。
func (s *Service) WorldTiles() map[string]config.WorldTileConfig {
	static := s.settings().WorldTiles
	out := maps.Clone(static)
	if out == nil {
		out = map[string]config.WorldTileConfig{}
	}
	done := map[string]bool{}
	for _, ss := range s.state.Load().sources {
		for _, w := range ss.worlds {
			if w.Layer == nil || done[w.Name] {
				continue
			}
			done[w.Name] = true
			t := s.effectiveTile(w.Name, w.Layer)
			t.TileURL = ProxyPath + "/" + url.PathEscape(ss.cfg.ID) + "/" + url.PathEscape(w.Name) + "/{z}/{x}/{y}"
			if own, ok := static[w.Name]; ok {
				t.Zoom, t.Opacity, t.Center = own.Zoom, own.Opacity, own.Center
				if own.MinZoom != 0 {
					t.MinZoom = max(own.MinZoom, t.MinZoom)
				}
				if own.MaxZoom != 0 {
					t.MaxZoom = own.MaxZoom
				}
			}
			out[w.Name] = t
		}
	}
	return out
}

// effectiveTile 返回外部地图的对齐参数，原生缩放下限扩展到该世界配置的合成层级（不低于 z0）。
func (s *Service) effectiveTile(world string, l Layer) config.WorldTileConfig {
	t := l.Tile()
	n := min(float64(s.settings().WorldTiles[world].SynthLevels()), t.MinNativeZoom)
	t.MinNativeZoom -= n
	t.MinZoom -= n
	return t
}

// layer 查找某来源某世界的底图。
func (s *Service) layer(sourceID, world string) (string, Layer, bool) {
	for _, ss := range s.state.Load().sources {
		if ss.cfg.ID != sourceID {
			continue
		}
		for _, w := range ss.worlds {
			if w.Name == world && w.Layer != nil {
				return w.Map, w.Layer, true
			}
		}
	}
	return "", nil, false
}

// Tile 是一张代理瓦片。
type Tile struct {
	Data        []byte
	ContentType string
}

// Fetch 返回某来源某世界的 XYZ 瓦片：命中缓存直接返回（过期也先返回旧瓦片，并在后台刷新），否则限流拉取
// 上游并写入缓存；上游最粗一级之上的层级由下一级 2×2 张瓦片合成（逐级递归，每级都缓存，只合成线路 / 车站
// 范围加边距以内的区域）。上游没有该瓦片时返回透明 PNG（同样缓存）。来源 / 世界未知或坐标越界返回 ErrNotFound。
func (s *Service) Fetch(ctx context.Context, sourceID, world string, z, x, y int) (Tile, error) {
	if z < 0 || z > maxZoomLevel || x < 0 || y < 0 || x >= 1<<z || y >= 1<<z {
		return Tile{}, ErrNotFound
	}
	mapName, layer, ok := s.layer(sourceID, world)
	if !ok {
		return Tile{}, ErrNotFound
	}
	own := s.settings().WorldTiles[world]
	req := &tileRequest{
		prefix: sourceID + "\x00" + world + "\x00" + mapName, layer: layer, native: layer.Tile(),
		synthLevels: own.SynthLevels(), margin: own.SynthMargin(),
	}
	if s.worldBounds != nil {
		if b, ok := s.worldBounds()[world]; ok {
			m := req.margin
			req.extent = &geo.Bounds{MinX: b.MinX - m, MinZ: b.MinZ - m, MaxX: b.MaxX + m, MaxZ: b.MaxZ + m}
		}
	}
	// 与发起请求的浏览器连接解耦：同一瓦片可能被多个请求共享，单个请求取消不应中止拉取。
	return s.fetchTile(context.WithoutCancel(ctx), req, z, x, y)
}

// tileRequest 是一次瓦片请求中不随层级变化的部分。
type tileRequest struct {
	prefix string // 缓存键前缀（来源 / 世界 / 地图）
	layer  Layer
	native config.WorldTileConfig // 上游原生对齐参数
	// synthLevels / margin 是该世界的合成层级数与合成范围边距
	synthLevels int
	margin      float64
	extent      *geo.Bounds // 合成范围（游戏坐标，已含边距）；nil 表示不限制
}

// outsideExtent 报告瓦片是否完全在合成范围之外（按 coords.ts 的线性映射把瓦片换算回游戏坐标）。
func (r *tileRequest) outsideExtent(z, x, y int) bool {
	if r.extent == nil {
		return false
	}
	t := r.native
	scale := t.MapScale
	if scale <= 0 {
		scale = 1
	}
	var offX, offZ float64
	if len(t.MapOffset) == 2 {
		offX, offZ = t.MapOffset[0], t.MapOffset[1]
	}
	// 以最精细一级（maxNativeZoom）的像素为单位：中心 = tileSize·2^N / 2，像素 = 中心 + (游戏坐标 + offset)·scale。
	n := int(t.MaxNativeZoom)
	center := float64(t.TileSize) * float64(int64(1)<<n) / 2
	span := float64(t.TileSize) * float64(int64(1)<<max(n-z, 0))
	toGame := func(px, off float64) float64 { return (px-center)/scale - off }
	minX, maxX := toGame(float64(x)*span, offX), toGame(float64(x+1)*span, offX)
	minZ, maxZ := toGame(float64(y)*span, offZ), toGame(float64(y+1)*span, offZ)
	e := r.extent
	return maxX < e.MinX || minX > e.MaxX || maxZ < e.MinZ || minZ > e.MaxZ
}

func (s *Service) fetchTile(ctx context.Context, req *tileRequest, z, x, y int) (Tile, error) {
	cfg := s.settings()
	synth := float64(z) < req.native.MinNativeZoom
	var key, upstream string
	if synth {
		if float64(z) < req.native.MinNativeZoom-float64(req.synthLevels) || req.outsideExtent(z, x, y) {
			return blankTile(), nil
		}
		// 合成瓦片的内容取决于合成范围，边距变化后不复用旧瓦片
		key = fmt.Sprintf("%s\x00m%g\x00%d/%d/%d", req.prefix, req.margin, z, x, y)
	} else {
		var ok bool
		if upstream, ok = req.layer.UpstreamURL(z, x, y); !ok {
			return blankTile(), nil
		}
		key = req.prefix + "\x00" + upstream
	}
	load := func() (any, error) {
		var t Tile
		var err error
		if synth {
			t, err = s.composeChildren(ctx, req, z, x, y, cfg.RenderJPEGQuality)
		} else {
			t, err = s.fetchUpstream(ctx, upstream)
		}
		if err != nil {
			return Tile{}, err
		}
		ttl := cfg.CacheTTLSeconds
		switch {
		case isBlank(t):
			ttl = cfg.BlankCacheTTLSeconds
		case synth:
			ttl = cfg.SynthCacheTTLSeconds
		}
		s.cache.put(&cacheEntry{
			key: key, data: t.Data, contentType: t.ContentType,
			expires: time.Now().Add(time.Duration(ttl) * time.Second),
		}, int64(cfg.CacheMaxMB)<<20)
		return t, nil
	}
	if e, stale, ok := s.cache.get(key, time.Now()); ok {
		if stale {
			// 先返回旧瓦片，后台刷新（singleflight 折叠重复刷新；失败时保留旧瓦片，下次访问再试）。
			go func() {
				if _, err, _ := s.flight.Do(key, load); err != nil {
					s.logger.Debug("Tile refresh failed", "err", err)
				}
			}()
		}
		return Tile{Data: e.data, ContentType: e.contentType}, nil
	}
	v, err, _ := s.flight.Do(key, load)
	if err != nil {
		return Tile{}, err
	}
	return v.(Tile), nil
}

// composeChildren 并发取下一级的 2×2 张瓦片并合成；合成范围外的子瓦片按空白处理、不访问上游；
// 任一子瓦片失败则整体失败（不缓存残缺瓦片）。
func (s *Service) composeChildren(ctx context.Context, req *tileRequest, z, x, y, quality int) (Tile, error) {
	var children [4]Tile
	errs := make(chan error, 4)
	for i := range children {
		cx, cy := 2*x+i%2, 2*y+i/2
		if req.outsideExtent(z+1, cx, cy) {
			children[i] = blankTile()
			errs <- nil
			continue
		}
		go func() {
			var err error
			children[i], err = s.fetchTile(ctx, req, z+1, cx, cy)
			errs <- err
		}()
	}
	for range children {
		if err := <-errs; err != nil {
			return Tile{}, err
		}
	}
	return composeTiles(children, req.native.TileSize, quality)
}

func (s *Service) fetchUpstream(ctx context.Context, upstream string) (Tile, error) {
	sem := s.semaphore()
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return Tile{}, ctx.Err()
	}
	body, ct, status, err := s.get(ctx, upstream, maxTileBytes)
	switch {
	case err != nil:
		return Tile{}, err
	case status == http.StatusNotFound || status == http.StatusNoContent:
		return blankTile(), nil
	case status != http.StatusOK:
		return Tile{}, fmt.Errorf("GET %s: HTTP %d", upstream, status)
	}
	// dynmap 的 MySQL/PHP 模式对不存在的瓦片可能返回 200 + 非图片内容，按空瓦片处理。
	if ct == "" {
		ct = http.DetectContentType(body)
	}
	if len(body) == 0 || !isImage(ct) {
		return blankTile(), nil
	}
	return Tile{Data: body, ContentType: ct}, nil
}

func isImage(contentType string) bool {
	return len(contentType) >= 6 && contentType[:6] == "image/"
}

func blankTile() Tile {
	return Tile{Data: transparentPNG, ContentType: "image/png"}
}

// semaphore 返回上游并发限制信号量；并发数配置变化时换新（已在途的请求仍归还到旧信号量）。
func (s *Service) semaphore() chan struct{} {
	n := s.settings().UpstreamConcurrency
	s.semMu.Lock()
	defer s.semMu.Unlock()
	if s.sem == nil || cap(s.sem) != n {
		s.sem = make(chan struct{}, n)
	}
	return s.sem
}

// BrowserMaxAge 返回代理瓦片的浏览器缓存秒数。
func (s *Service) BrowserMaxAge() int {
	return s.settings().BrowserCacheSeconds
}

// Status 是各来源的发现状态（供服务器配置页展示世界匹配情况）。
type Status struct {
	Sources    []SourceStatus `json:"sources"`
	CacheItems int            `json:"cacheItems"`
	CacheBytes int64          `json:"cacheBytes"`
}

type SourceStatus struct {
	ID           string        `json:"id"`
	Type         string        `json:"type"`
	URL          string        `json:"url"`
	Error        string        `json:"error,omitempty"`
	DiscoveredAt int64         `json:"discoveredAt,omitempty"`
	Worlds       []WorldStatus `json:"worlds"`
}

type WorldStatus struct {
	Name string `json:"name"`
	Map  string `json:"map,omitempty"`
	Skip string `json:"skip,omitempty"`
	// Tile 是推导的底图参数（含合成层级后的原生缩放、缩放范围），供配置页展示默认值；不可用时为 nil。
	Tile *config.WorldTileConfig `json:"tile,omitempty"`
}

// Status 返回当前发现状态。
func (s *Service) Status() Status {
	st := Status{Sources: []SourceStatus{}}
	for _, ss := range s.state.Load().sources {
		src := SourceStatus{ID: ss.cfg.ID, Type: ss.cfg.Type, URL: ss.cfg.URL, Error: ss.err, Worlds: []WorldStatus{}}
		if !ss.discoveredAt.IsZero() {
			src.DiscoveredAt = ss.discoveredAt.UnixMilli()
		}
		for _, w := range ss.worlds {
			ws := WorldStatus{Name: w.Name, Map: w.Map, Skip: w.Skip}
			if w.Layer != nil {
				t := s.effectiveTile(w.Name, w.Layer)
				ws.Tile = &t
			}
			src.Worlds = append(src.Worlds, ws)
		}
		st.Sources = append(st.Sources, src)
	}
	st.CacheItems, st.CacheBytes = s.cache.stats()
	return st
}

// ParseTileCoords 解析代理路径中的 z / x / y。
func ParseTileCoords(z, x, y string) (int, int, int, bool) {
	zi, err1 := strconv.Atoi(z)
	xi, err2 := strconv.Atoi(x)
	yi, err3 := strconv.Atoi(y)
	return zi, xi, yi, err1 == nil && err2 == nil && err3 == nil
}
