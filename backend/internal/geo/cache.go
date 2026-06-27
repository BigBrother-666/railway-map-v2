// Package geo 持有 geojson / 线路 / 系统的内存缓存，并落库以便插件断连时降级供数
// （见 docs/BACKEND_PROMPT.md §6、§12）。
package geo

import (
	"crypto/sha1"
	"encoding/hex"
	"sync"

	"railway-map-backend/internal/store"
)

// Cache 是 geo 数据的并发安全缓存。
type Cache struct {
	mu sync.RWMutex

	geojson    []byte // 合并后的 FeatureCollection 原始 JSON
	geoVersion string
	lines      []byte // 线路数组 JSON
	systems    []byte // 系统数组 JSON

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
