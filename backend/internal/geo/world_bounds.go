package geo

import (
	"encoding/json"
	"math"
)

// Bounds 是游戏平面坐标范围（x / z，含端点）。
type Bounds struct {
	MinX, MinZ, MaxX, MaxZ float64
}

// WorldBounds 返回各世界线路与车站坐标的范围（外部地图只合成有数据的范围用）；geojson 版本变化时惰性重建。
func (c *Cache) WorldBounds() map[string]Bounds {
	c.mu.RLock()
	if c.worldBounds != nil && c.worldBoundsVersion == c.geoVersion {
		b := c.worldBounds
		c.mu.RUnlock()
		return b
	}
	payload, version := c.geojson, c.geoVersion
	c.mu.RUnlock()

	b := buildWorldBounds(payload)
	c.mu.Lock()
	if c.geoVersion == version {
		c.worldBounds, c.worldBoundsVersion = b, version
	}
	c.mu.Unlock()
	return b
}

func buildWorldBounds(payload []byte) map[string]Bounds {
	out := map[string]Bounds{}
	var fc struct {
		Features []rawFeature `json:"features"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &fc) != nil {
		return out
	}
	extend := func(world string, c []float64) {
		if world == "" || len(c) < 2 {
			return
		}
		b, ok := out[world]
		if !ok {
			b = Bounds{MinX: math.Inf(1), MinZ: math.Inf(1), MaxX: math.Inf(-1), MaxZ: math.Inf(-1)}
		}
		b.MinX, b.MaxX = min(b.MinX, c[0]), max(b.MaxX, c[0])
		b.MinZ, b.MaxZ = min(b.MinZ, c[1]), max(b.MaxZ, c[1])
		out[world] = b
	}
	for _, f := range fc.Features {
		switch f.Geometry.Type {
		case "Point":
			var c []float64
			if json.Unmarshal(f.Geometry.Coordinates, &c) == nil {
				extend(f.Properties.World, c)
			}
		case "LineString":
			var cs [][]float64
			if json.Unmarshal(f.Geometry.Coordinates, &cs) == nil {
				for _, c := range cs {
					extend(f.Properties.World, c)
				}
			}
		}
	}
	return out
}
