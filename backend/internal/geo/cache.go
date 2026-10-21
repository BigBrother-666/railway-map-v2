// Package geo 持有 geojson / 线路 / 系统 / 车站别名与站组的内存缓存，并落库以便插件断连时降级供数
// （见 docs/BACKEND_PROMPT.md §6、§12）。
package geo

import (
	"crypto/sha1"
	"encoding/hex"
	"sync"

	"railway-map-backend/internal/model"
	"railway-map-backend/internal/store"
)

// Cache 是 geo 数据的并发安全缓存。
type Cache struct {
	mu sync.RWMutex

	geojson    []byte // 合并后的 FeatureCollection 原始 JSON
	geoVersion string
	lines      []byte // 线路数组 JSON
	systems    []byte // 系统数组 JSON
	// stations 是车站英文名映射与站组的原始 JSON（形如 {"aliases":{...},"groups":{...}}）
	stations        []byte
	stationsVersion string

	// 私有快照（见 private.go）
	privateSystems []model.PrivateSystem
	privateLines   []model.PrivateLine

	// geoIdx 是 geojson 的轨道段 / 节点坐标索引（乘车历史坐标快照用），按 geoIdxVersion 惰性重建
	geoIdx        *geoIndex
	geoIdxVersion string

	// worldBounds 是各世界线路 / 车站坐标范围（见 world_bounds.go），按 worldBoundsVersion 惰性重建
	worldBounds        map[string]Bounds
	worldBoundsVersion string

	store *store.Store
}

// NewCache 创建缓存，并尝试从库里恢复上次快照（重启后无需等插件即可供数）。
func NewCache(st *store.Store) *Cache {
	c := &Cache{store: st}
	c.restore()
	return c
}

func (c *Cache) restore() {
	if c.store == nil {
		return
	}
	if snap, err := c.store.LoadSnapshot("geo"); err == nil && snap != nil {
		c.geojson = snap.Payload
		c.geoVersion = snap.Version
	}
	if snap, err := c.store.LoadSnapshot("lines"); err == nil && snap != nil {
		c.lines = snap.Payload
	}
	if snap, err := c.store.LoadSnapshot("systems"); err == nil && snap != nil {
		c.systems = snap.Payload
	}
	if snap, err := c.store.LoadSnapshot("stations"); err == nil && snap != nil {
		c.stations = snap.Payload
		c.stationsVersion = snap.Version
	}
	c.restorePrivate()
}

// SetGeojson 整体替换 geojson 缓存并落库，版本号取内容的 sha1 前缀。
func (c *Cache) SetGeojson(featureCollection []byte) {
	version := shortHash(featureCollection)
	c.mu.Lock()
	c.geojson = featureCollection
	c.geoVersion = version
	c.mu.Unlock()
	c.persist("geo", version, featureCollection)
}

// SetLines / SetSystems 整体替换线路 / 系统缓存并落库。
func (c *Cache) SetLines(lines []byte) {
	c.mu.Lock()
	c.lines = lines
	c.mu.Unlock()
	c.persist("lines", "", lines)
}

func (c *Cache) SetSystems(systems []byte) {
	c.mu.Lock()
	c.systems = systems
	c.mu.Unlock()
	c.persist("systems", "", systems)
}

// SetStations 整体替换车站英文名 / 站组缓存并落库，版本号取内容的 sha1 前缀（供寻路结果缓存失效判断）。
func (c *Cache) SetStations(stations []byte) {
	version := shortHash(stations)
	c.mu.Lock()
	c.stations = stations
	c.stationsVersion = version
	c.mu.Unlock()
	c.persist("stations", version, stations)
}

// Stations 返回车站英文名 / 站组的原始 JSON（可能为 nil）及其版本号。
func (c *Cache) Stations() ([]byte, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stations, c.stationsVersion
}

func (c *Cache) persist(key, version string, payload []byte) {
	if c.store != nil {
		_ = c.store.SaveSnapshot(key, version, payload)
	}
}

// Geojson 返回合并后的 FeatureCollection 原始 JSON 与版本号。
func (c *Cache) Geojson() ([]byte, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.geojson, c.geoVersion
}

// GeoVersion 返回当前 geo 版本号。
func (c *Cache) GeoVersion() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.geoVersion
}

// Lines / Systems 返回线路 / 系统数组的原始 JSON（可能为 nil）。
func (c *Cache) Lines() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lines
}

func (c *Cache) Systems() []byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.systems
}

// shortHash 取内容 sha1 的前 12 个十六进制字符作为版本号 / ETag。
func shortHash(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])[:12]
}
