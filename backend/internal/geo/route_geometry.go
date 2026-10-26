package geo

import (
	"encoding/json"
	"math"

	"railway-map-backend/internal/model"
)

// edgeGeom 是 geojson 中一条有向轨道段的几何与属性（供乘车历史坐标快照使用）。
type edgeGeom struct {
	lineID string
	world  string
	length float64     // 米
	coords [][]float64 // 游戏坐标 [x, z]
}

// geoIndex 是由 geojson 解析出的「from__to → 轨道段」与「节点 id → 坐标」索引。
// 共线段（同一物理轨道上有多条线路）在 geojson 里每条线路各一个 from/to 相同的要素，故 edges 为一对多。
type geoIndex struct {
	edges map[string][]edgeGeom
	nodes map[string][]float64
}

// rawFeature 用于宽松解析 geojson 要素（仅取快照需要的字段）。
type rawFeature struct {
	Properties struct {
		ID     string  `json:"id"`
		From   string  `json:"from"`
		To     string  `json:"to"`
		LineID string  `json:"lineId"`
		World  string  `json:"world"`
		Length float64 `json:"length"`
	} `json:"properties"`
	Geometry struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
}

// buildGeoIndex 解析合并后的 FeatureCollection，构建轨道段与节点坐标索引。
//
// @param payload geojson FeatureCollection 原始 JSON
// @return 索引；解析失败返回空索引
func buildGeoIndex(payload []byte) *geoIndex {
	idx := &geoIndex{edges: map[string][]edgeGeom{}, nodes: map[string][]float64{}}
	var fc struct {
		Features []rawFeature `json:"features"`
	}
	if len(payload) == 0 || json.Unmarshal(payload, &fc) != nil {
		return idx
	}
	for _, f := range fc.Features {
		p := f.Properties
		switch f.Geometry.Type {
		case "Point":
			var c []float64
			if p.ID != "" && json.Unmarshal(f.Geometry.Coordinates, &c) == nil && len(c) >= 2 {
				idx.nodes[p.ID] = []float64{c[0], c[1]}
			}
		case "LineString":
			var cs [][]float64
			if p.From == "" || p.To == "" || json.Unmarshal(f.Geometry.Coordinates, &cs) != nil {
				continue
			}
			coords := make([][]float64, 0, len(cs))
			for _, c := range cs {
				if len(c) >= 2 {
					coords = append(coords, []float64{c[0], c[1]})
				}
			}
			key := p.From + "__" + p.To
			if hasLine(idx.edges[key], p.LineID) {
				continue // 同一条边被两个遍历单元各产出一份
			}
			idx.edges[key] = append(idx.edges[key], edgeGeom{
				lineID: p.LineID, world: p.World, length: p.Length, coords: coords,
			})
		}
	}
	return idx
}

// index 返回当前 geojson 对应的索引；geojson 版本变化时惰性重建。
func (c *Cache) index() *geoIndex {
	c.mu.RLock()
	if c.geoIdx != nil && c.geoIdxVersion == c.geoVersion {
		idx := c.geoIdx
		c.mu.RUnlock()
		return idx
	}
	payload, version := c.geojson, c.geoVersion
	c.mu.RUnlock()

	idx := buildGeoIndex(payload)
	c.mu.Lock()
	if c.geoVersion == version {
		c.geoIdx = idx
		c.geoIdxVersion = version
	}
	c.mu.Unlock()
	return idx
}

// RouteGeometry 按节点序列从当前 geojson 截取完整线路坐标快照（相邻同线路的段合并为一条折线）。
// 颜色取线路配置（不用 geojson 的 color，改线路颜色后无需插件重新遍历）。
// 用于乘车历史：即使之后 geojson 更新（中间增删节点、线路删除），历史仍可按当时的线形显示。
//
// @param nodeIDs 路线节点 id 序列
// @param segmentLineIDs 逐段 lineId（可空，与 nodeIDs 相邻对一一对应）：命中候选时共线段直接取该线路
// @param lineIDs 本次乘车经过的线路（可空）：无逐段信息时共线段只在这些线路里选边
// @return geom 坐标快照（一段都找不到时为 nil）；meters 找到的轨道段总长；complete 是否所有相邻节点对都找到了轨道段
func (c *Cache) RouteGeometry(nodeIDs, segmentLineIDs, lineIDs []string) (geom *model.RouteGeometry, meters float64, complete bool) {
	if len(nodeIDs) < 2 {
		return nil, 0, false
	}
	idx := c.index()
	colors := c.lineColors()
	g := &model.RouteGeometry{}
	complete = true
	var cur *model.RouteGeometrySegment
	for _, e := range pickRouteEdges(idx, nodeIDs, segmentLineIDs, lineIDs) {
		if e == nil || len(e.coords) < 2 {
			complete = false
			cur = nil // 缺段处断开折线，避免直线连接跨越未知区域
			continue
		}
		meters += e.length
		if g.World == "" {
			g.World = e.world
		}
		if cur != nil && cur.LineID == e.lineID {
			cur.Coords = appendCoords(cur.Coords, e.coords)
			continue
		}
		g.Segments = append(g.Segments, model.RouteGeometrySegment{
			LineID: e.lineID, Color: lineColorOr(colors, e.lineID), Coords: appendCoords(nil, e.coords),
		})
		cur = &g.Segments[len(g.Segments)-1]
	}
	if len(g.Segments) == 0 {
		return nil, meters, false
	}
	for i := range g.Segments {
		g.Segments[i].Coords = simplifyCollinear(g.Segments[i].Coords)
	}
	g.Start = idx.nodes[nodeIDs[0]]
	g.End = idx.nodes[nodeIDs[len(nodeIDs)-1]]
	return g, meters, complete
}

// pickRouteEdges 为节点序列的每一段选一条轨道段（与前端 routing/routeEdges.ts 的 resolveRouteEdges 同一规则）：
// hints 第 i 项命中第 i 段候选时直接取该线路；否则在候选里选「从本段起能沿路线连续覆盖最多段」的线路，
// 平手沿用上一段的线路；lineIDs 非空且候选中有命中时只在命中者里选。缺段返回 nil。
func pickRouteEdges(idx *geoIndex, nodeIDs, hints, lineIDs []string) []*edgeGeom {
	n := len(nodeIDs) - 1
	if n <= 0 {
		return nil
	}
	if len(hints) != n {
		hints = nil // 长度对不上节点序列的提示无法逐段对齐，整体忽略
	}
	cands := make([][]edgeGeom, n)
	for i := 0; i < n; i++ {
		c := idx.edges[nodeIDs[i]+"__"+nodeIDs[i+1]]
		if len(lineIDs) > 0 && len(c) > 1 {
			var filtered []edgeGeom
			for _, e := range c {
				if contains(lineIDs, e.lineID) {
					filtered = append(filtered, e)
				}
			}
			if len(filtered) > 0 {
				c = filtered
			}
		}
		cands[i] = c
	}
	// runs[i]：lineID → 从第 i 段起该线路连续出现在候选中的段数（倒序递推）
	runs := make([]map[string]int, n+1)
	runs[n] = map[string]int{}
	for i := n - 1; i >= 0; i-- {
		runs[i] = make(map[string]int, len(cands[i]))
		for _, e := range cands[i] {
			runs[i][e.lineID] = 1 + runs[i+1][e.lineID]
		}
	}
	out := make([]*edgeGeom, n)
	prev := ""
	for i := 0; i < n; i++ {
		best, bestRun := -1, -1
		for j, e := range cands[i] {
			if hints != nil && hints[i] != "" && e.lineID == hints[i] {
				best = j
				break
			}
			run := runs[i][e.lineID]
			if run > bestRun || (run == bestRun && e.lineID == prev) {
				best, bestRun = j, run
			}
		}
		if best < 0 {
			continue
		}
		out[i] = &cands[i][best]
		prev = out[i].lineID
	}
	return out
}

func hasLine(edges []edgeGeom, lineID string) bool {
	for _, e := range edges {
		if e.lineID == lineID {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// DefaultLineColor 是线路配置中找不到线路时的兜底颜色（与插件 LineConfig.getColor 一致）。
const DefaultLineColor = "#a9a9a9"

// lineColors 返回线路 id → 配置颜色（取自线路私有快照，含联络线 contact）。
func (c *Cache) lineColors() map[string]string {
	lines := c.PrivateLines()
	m := make(map[string]string, len(lines))
	for _, l := range lines {
		if l.Color != "" {
			m[l.ID] = l.Color
		}
	}
	return m
}

func lineColorOr(colors map[string]string, lineID string) string {
	if c, ok := colors[lineID]; ok {
		return c
	}
	return DefaultLineColor
}

// appendCoords 把一段坐标追加到折线末尾（与末点重合的首点去重），坐标保留 2 位小数。
func appendCoords(dst, src [][]float64) [][]float64 {
	for _, c := range src {
		p := []float64{round2(c[0]), round2(c[1])}
		if n := len(dst); n > 0 && dst[n-1][0] == p[0] && dst[n-1][1] == p[1] {
			continue
		}
		dst = append(dst, p)
	}
	return dst
}

// simplifyCollinear 删除同向共线的中间点（铁轨多为直线，可大幅减少快照体积且线形不变）。
func simplifyCollinear(coords [][]float64) [][]float64 {
	if len(coords) < 3 {
		return coords
	}
	out := [][]float64{coords[0]}
	for i := 1; i < len(coords)-1; i++ {
		a, b, c := out[len(out)-1], coords[i], coords[i+1]
		dx1, dz1 := b[0]-a[0], b[1]-a[1]
		dx2, dz2 := c[0]-b[0], c[1]-b[1]
		cross := dx1*dz2 - dz1*dx2
		dot := dx1*dx2 + dz1*dz2
		if math.Abs(cross) < 1e-6 && dot > 0 {
			continue
		}
		out = append(out, b)
	}
	return append(out, coords[len(coords)-1])
}

// round2 保留 2 位小数。
func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
