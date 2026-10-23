// Package routing 提供基于 geojson 的寻路图构建与最短路 / K-最短路 / 联程票搜索、票价估算与
// 结果排序。端口自前端 frontend/src/routing/{graph,pathfind,ranker,fare,compute}.ts，与插件
// GeoRouteEngine / TicketRanker / ThroughTicket 语义对齐——三处任一修改寻路/购票逻辑都需要同步。
//
// 本包持有的 Graph 是一份独立于 internal/geo.Cache 的、专用于服务端寻路计算的图结构；
// geo.Cache 继续原样透传 geojson 字节给前端用于地图渲染与车站/线路详情展示，两者职责不同、互不影响。
package routing

import (
	"encoding/json"
	"math"
	"sync"

	"railway-map-backend/internal/model"
)

// Link 是图中的一条有向边。
type Link struct {
	From      string
	To        string
	LineID    string
	Distance  float64 // 米
	SystemID  string
	DepartDir string
	// EnterFrom 是入向面门控：到达起点道岔的允许到达面集合（与插件 GeoLink.enterFacesFrom 同源）。空表示不门控。
	EnterFrom []string
	// EnterTo 是沿本段到达终点节点的到达面 key（与插件 GeoLink.enterFaceTo 同源）。nil 表示缺失（放行）。
	EnterTo *string
	// Track 是正线 / 停靠线标记（geojson track）："main" 正线、"siding" 停靠线，普通段为空。
	Track string
}

// IsSiding 本段是否为停靠线（进站道岔第三行出向起到第一个 platform 为止）。
func (l *Link) IsSiding() bool { return l.Track == "siding" }

// IsMainline 本段是否为正线（进站道岔第四行出向，跨站直通到出站道岔）。
func (l *Link) IsMainline() bool { return l.Track == "main" }

// Node 是图中的一个节点。
type Node struct {
	ID        string
	Type      string // "station" | "switch"
	Name      string
	LineIDs   map[string]struct{}
	SystemIDs map[string]struct{}
	X, Y, Z   float64
}

// Graph 是寻路图：节点表 + 出边邻接表 + 车站名索引。由 BuildFromGeoJSON 构建完成后只读，
// 下面的派生缓存都用 sync.Once 惰性构建一次，可被多个查询并发使用。
type Graph struct {
	Nodes      map[string]*Node
	adjacency  map[string][]Link
	stationIdx map[string][]string // 车站名 → 该名下所有 station 节点 id（一个车站可能多站台）

	ixOnce              sync.Once
	ix                  *searchIndex      // 寻路热路径用的整数化索引，见 index.go
	mainlineSwitchCache map[string]string // 「进站道岔|lineId → 停靠线车站名」，随 ix 一起构建

	distOnce         sync.Once
	stationDistCache map[string]map[string]float64 // stationDirectDistances 的缓存
}

func newGraph() *Graph {
	return &Graph{
		Nodes:      make(map[string]*Node),
		adjacency:  make(map[string][]Link),
		stationIdx: make(map[string][]string),
	}
}

type geoPointProps struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	Name             string   `json:"name,omitempty"`
	LineIDs          []string `json:"lineIds,omitempty"`
	RailwaySystemIDs []string `json:"railwaySystemIds,omitempty"`
}

type geoLineProps struct {
	ID              string   `json:"id"`
	From            string   `json:"from"`
	To              string   `json:"to"`
	LineID          string   `json:"lineId"`
	RailwaySystemID string   `json:"railwaySystemId,omitempty"`
	Length          float64  `json:"length"`
	DepartDir       string   `json:"departDir,omitempty"`
	EnterFrom       []string `json:"enterFrom,omitempty"`
	EnterTo         *string  `json:"enterTo,omitempty"`
	Track           string   `json:"track,omitempty"`
}

type geoGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

// BuildFromGeoJSON 由 geojson FeatureCollection 反向构建寻路图（复刻 RouteGraph.fromFeatureCollection）。
func BuildFromGeoJSON(fc *model.FeatureCollection) (*Graph, error) {
	g := newGraph()
	if fc == nil {
		return g, nil
	}
	// 先加节点
	for _, f := range fc.Features {
		var geom geoGeometry
		if err := json.Unmarshal(f.Geometry, &geom); err != nil || geom.Type != "Point" {
			continue
		}
		var raw []float64
		if err := json.Unmarshal(geom.Coordinates, &raw); err != nil || len(raw) < 2 {
			continue
		}
		var p geoPointProps
		if err := json.Unmarshal(f.Properties, &p); err != nil || p.ID == "" {
			continue
		}
		y := 0.0
		if len(raw) > 2 {
			y = raw[2]
		}
		g.addNode(&Node{
			ID:        p.ID,
			Type:      p.Type,
			Name:      p.Name,
			LineIDs:   toSet(p.LineIDs),
			SystemIDs: toSet(p.RailwaySystemIDs),
			X:         raw[0],
			Z:         raw[1],
			Y:         y,
		})
	}
	// 再加边（节点已就位，便于把 lineId 累积到两端）
	for _, f := range fc.Features {
		var geom geoGeometry
		if err := json.Unmarshal(f.Geometry, &geom); err != nil || geom.Type != "LineString" {
			continue
		}
		var l geoLineProps
		if err := json.Unmarshal(f.Properties, &l); err != nil || l.From == "" || l.To == "" {
			continue
		}
		g.addLink(Link{
			From: l.From, To: l.To, LineID: l.LineID, Distance: l.Length,
			SystemID: l.RailwaySystemID, DepartDir: l.DepartDir,
			EnterFrom: l.EnterFrom, EnterTo: l.EnterTo, Track: l.Track,
		})
	}
	return g, nil
}

func toSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, it := range items {
		s[it] = struct{}{}
	}
	return s
}

func (g *Graph) addNode(n *Node) {
	if existing, ok := g.Nodes[n.ID]; ok {
		for id := range n.LineIDs {
			existing.LineIDs[id] = struct{}{}
		}
		for id := range n.SystemIDs {
			existing.SystemIDs[id] = struct{}{}
		}
		return
	}
	g.Nodes[n.ID] = n
	if n.Type == "station" && n.Name != "" {
		g.stationIdx[n.Name] = append(g.stationIdx[n.Name], n.ID)
	}
}

func (g *Graph) addLink(l Link) {
	g.adjacency[l.From] = append(g.adjacency[l.From], l)
	if n, ok := g.Nodes[l.From]; ok {
		n.LineIDs[l.LineID] = struct{}{}
	}
	if n, ok := g.Nodes[l.To]; ok {
		n.LineIDs[l.LineID] = struct{}{}
	}
}

// Links 返回节点的出边。
func (g *Graph) Links(nodeID string) []Link {
	return g.adjacency[nodeID]
}

// StationNodes 返回车站名下所有 station 节点 id。
func (g *Graph) StationNodes(name string) []string {
	return g.stationIdx[name]
}

// AllStationNames 返回所有车站名。
func (g *Graph) AllStationNames() []string {
	names := make([]string, 0, len(g.stationIdx))
	for name := range g.stationIdx {
		names = append(names, name)
	}
	return names
}

// platformNameOfMainlineSwitch 复刻插件 GeoRouteGraph.platformNameOfMainlineSwitch：
// 若 nodeID 是 lineID 的进站道岔（本线有 track=main 正线出边），返回沿本线停靠线（track=siding）出边
// 走到的第一个车站名；停靠线中途可以经过别的道岔。不是进站道岔返回空串。
func (g *Graph) platformNameOfMainlineSwitch(nodeID, lineID string) string {
	node := g.Nodes[nodeID]
	if node == nil || node.Type == "station" || lineID == "" {
		return ""
	}
	return g.mainlineSwitchStations()[nodeID+"|"+lineID]
}

// mainlineSwitchStations 返回「进站道岔|lineId → 停靠线车站名」索引（随寻路索引一起惰性构建一次）。
func (g *Graph) mainlineSwitchStations() map[string]string {
	g.index()
	return g.mainlineSwitchCache
}

// buildMainlineSwitchStations 构建「进站道岔|lineId → 停靠线车站名」索引。
func (g *Graph) buildMainlineSwitchStations() map[string]string {
	result := make(map[string]string)
	for from, links := range g.adjacency {
		for i := range links {
			if !links[i].IsMainline() {
				continue
			}
			if name := g.sidingStationFrom(from, links[i].LineID); name != "" {
				result[from+"|"+links[i].LineID] = name
			}
		}
	}
	return result
}

// sidingStationFrom 从进站道岔沿本线停靠线出边 BFS，返回遇到的第一个车站名；找不到返回空串。
func (g *Graph) sidingStationFrom(switchID, lineID string) string {
	queue := []string{switchID}
	seen := map[string]bool{switchID: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, l := range g.Links(cur) {
			if !l.IsSiding() || l.LineID != lineID {
				continue
			}
			to := g.Nodes[l.To]
			if to == nil {
				continue
			}
			if to.Type == "station" {
				if to.Name != "" {
					return to.Name
				}
			} else if !seen[to.ID] {
				seen[to.ID] = true
				queue = append(queue, to.ID)
			}
		}
	}
	return ""
}

// stationDirectDistances 站名级「直达可达」缩合距离矩阵：起点站名 → 终点站名 → 一趟快速车的最短距离（km）。
// 复刻 RouteGraph.stationDirectDistances：口径为下界估计（忽略 enterFace / 折返 / 停靠线中途站约束），
// 只用于 FindTransferJourneys 筛选候选，最终每段仍由 FindByStation 权威实体化施加全部约束。
// 首次调用时构建（并发安全，只构建一次）。
func (g *Graph) stationDirectDistances() map[string]map[string]float64 {
	g.distOnce.Do(func() { g.stationDistCache = g.buildStationDirectDistances() })
	return g.stationDistCache
}

func (g *Graph) buildStationDirectDistances() map[string]map[string]float64 {
	ix := g.index()
	dist := make([]float64, len(ix.nodeIDs))
	h := &idxHeap{}
	matrix := make(map[string]map[string]float64, len(g.stationIdx))
	for startName, platforms := range g.stationIdx {
		row := make(map[string]float64)
		for _, platformID := range platforms {
			ix.accumulateShortestToStations(ix.nodeIdx[platformID], row, dist, h)
		}
		delete(row, startName) // 起点到自身不算直达候选
		matrix[startName] = row
	}
	return matrix
}

// accumulateShortestToStations 从单一起点节点做普通 Dijkstra（边权 = 段长，米），
// 把到达各站名的最短距离（km）并入 out（取更小值）。dist / h 是调用方复用的缓冲区。
func (ix *searchIndex) accumulateShortestToStations(start int32, out map[string]float64, dist []float64, h *idxHeap) {
	for i := range dist {
		dist[i] = math.Inf(1)
	}
	h.items = h.items[:0]
	dist[start] = 0
	h.push(idxItem{i: start, d: 0})
	for h.size() > 0 {
		cur := h.pop()
		if cur.d > dist[cur.i] {
			continue
		}
		if name := ix.nodeName[cur.i]; name >= 0 && cur.d > 0 {
			km := cur.d / 1000
			if prev, ok := out[ix.names[name]]; !ok || km < prev {
				out[ix.names[name]] = km
			}
		}
		for _, li := range ix.out[cur.i] {
			to := ix.linkTo[li]
			if nd := cur.d + ix.links[li].Distance; nd < dist[to] {
				dist[to] = nd
				h.push(idxItem{i: to, d: nd})
			}
		}
	}
}
