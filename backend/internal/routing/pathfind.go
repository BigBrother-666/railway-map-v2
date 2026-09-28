package routing

import (
	"context"
	"sort"
	"strings"

	"railway-map-backend/internal/model"
)

// ReversePredicate 判断 (lineId, stationName) 是否为折返站。
type ReversePredicate func(lineID, stationName string) bool

// kspSafetyCap 是未限制条数时每站台 K-最短路的安全上限（复刻插件 GeoRouteEngine）。
const kspSafetyCap = 16

// maxPops 是单次 kShortest 优先队列的最大出队次数安全阀，防止无解图上无限循环。
const maxPops = 200_000

type entry struct {
	nodeID string
	dist   float64
	link   *Link
	prev   *entry
}

// minHeap 是按 dist 排序的二叉最小堆，复刻插件 java.util.PriorityQueue：push/pop 均 O(log n)。
type minHeap struct {
	items []*entry
}

func (h *minHeap) push(e *entry) {
	h.items = append(h.items, e)
	i := len(h.items) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if h.items[parent].dist <= h.items[i].dist {
			break
		}
		h.items[parent], h.items[i] = h.items[i], h.items[parent]
		i = parent
	}
}

func (h *minHeap) pop() *entry {
	top := h.items[0]
	last := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	if len(h.items) > 0 {
		h.items[0] = last
		i := 0
		n := len(h.items)
		for {
			l, r := 2*i+1, 2*i+2
			s := i
			if l < n && h.items[l].dist < h.items[s].dist {
				s = l
			}
			if r < n && h.items[r].dist < h.items[s].dist {
				s = r
			}
			if s == i {
				break
			}
			h.items[s], h.items[i] = h.items[i], h.items[s]
			i = s
		}
	}
	return top
}

func (h *minHeap) size() int { return len(h.items) }

// FindByStation 按起点站名 + 终点站名求候选路线（距离升序、两级去重）。复刻插件
// GeoRouteEngine.findByStation：枚举起点各站台各求 K 条 → 一级按 departDirectionSequence 去重 →
// 二级按 stationSequence 去重，择优规则 isBetterRoute（转线次数少者优先，相同则距离短者）。
func FindByStation(ctx context.Context, g *Graph, startStation, endStation string, maxResults int, isReverse ReversePredicate) []model.RoutePath {
	kPerPlatform := maxResults
	if kPerPlatform <= 0 {
		kPerPlatform = kspSafetyCap
	}
	var all []model.RoutePath
	for _, startID := range g.StationNodes(startStation) {
		all = append(all, kShortest(ctx, g, startID, endStation, kPerPlatform, isReverse)...)
		if ctx.Err() != nil {
			// ctx 已取消/超时：不再枚举剩余站台，但已经找到的路径（如果有）仍是真实完整的路径，
			// 继续走下面的去重/排序/截断逻辑返回它们，而不是把已经算出来的结果也一并丢弃。
			break
		}
	}

	// 一级去重：departDirectionSequence 相同视为重复路线，保留 isBetterRoute 更优者
	deduped := make(map[string]model.RoutePath)
	for _, p := range all {
		key := departDirectionKey(p)
		if old, ok := deduped[key]; !ok || isBetterRoute(p, old) {
			deduped[key] = p
		}
	}

	// 二级去重：stationSequence（经过车站序列）相同也视为同一路线
	byStations := make(map[string]model.RoutePath)
	for _, p := range deduped {
		key := strings.Join(p.Stations, ">")
		if old, ok := byStations[key]; !ok || isBetterRoute(p, old) {
			byStations[key] = p
		}
	}

	ret := make([]model.RoutePath, 0, len(byStations))
	for _, p := range byStations {
		ret = append(ret, p)
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].Distance < ret[j].Distance })
	if maxResults > 0 && len(ret) > maxResults {
		ret = ret[:maxResults]
	}
	return ret
}

func departDirectionKey(path model.RoutePath) string {
	return strings.Join(path.DepartDirectionSequence, ">")
}

// isBetterRoute 择优规则：转线次数少者优先；相同则距离短者优先。candidate 应取代 current 时返回 true。
func isBetterRoute(candidate, current model.RoutePath) bool {
	candTransfers := lineTransferCount(candidate.LineIDSequence)
	curTransfers := lineTransferCount(current.LineIDSequence)
	if candTransfers != curTransfers {
		return candTransfers < curTransfers
	}
	return candidate.Distance < current.Distance
}

func lineTransferCount(seq []string) int {
	count := 0
	for i := 0; i < len(seq)-1; i++ {
		if seq[i] != seq[i+1] {
			count++
		}
	}
	return count
}

// kShortest 从单一起点节点求 K 条无环最短路线，终点为任一名为 endStation 的 station 节点。
// 优先队列按累计距离扩展，跳过已在当前路径前缀中的节点保证无环（允许回到起点闭合环线）。
func kShortest(ctx context.Context, g *Graph, startID, endStation string, k int, isReverse ReversePredicate) []model.RoutePath {
	var results []model.RoutePath
	startNode := g.Nodes[startID]
	if startNode == nil || k < 1 {
		return results
	}
	var startStation string
	if startNode.Type == "station" {
		startStation = startNode.Name
	}

	// 快速短路：先用一次忽略「同名站不可重复经过」约束的 Dijkstra 判断可达性。忽略该约束只会让
	// 判定更宽松，故返回不可达时，下面完整约束下的枚举也必然找不到任何合法路径——可安全跳过，
	// 避免「两站根本无解」时把整片死胡同路径空间穷举到 MAX_POPS 才放弃（可能耗时数十秒）。
	if !reachableIgnoringRevisit(ctx, g, startID, endStation, isReverse) {
		return results
	}
	if testKShortestFullSearchHook != nil {
		testKShortestFullSearchHook()
	}

	pq := &minHeap{}
	pq.push(&entry{nodeID: startID, dist: 0})
	pops := 0

	for pq.size() > 0 && len(results) < k && pops < maxPops {
		if pops%4096 == 0 && ctx.Err() != nil {
			return results
		}
		cur := pq.pop()
		pops++

		curNode := g.Nodes[cur.nodeID]
		if curNode != nil && curNode.Type == "station" && curNode.Name == endStation && cur.link != nil {
			results = append(results, buildPath(g, cur))
			if testKShortestResultFoundHook != nil {
				testKShortestResultFoundHook()
			}
			continue
		}
		for _, link := range g.Links(cur.nodeID) {
			link := link
			nextID := link.To
			nextNode := g.Nodes[nextID]
			if nextNode == nil {
				continue
			}
			// 入向面门控：与插件 enterFaceAllows 一致，拒绝「从错误到达面接反向牌出边」的非法接续
			if !enterFaceAllows(cur.link, &link) {
				continue
			}
			var curStationName string
			hasCurStationName := false
			if curNode != nil && curNode.Type != "station" {
				curStationName = getNodeStationName(g, cur.nodeID, link.LineID)
				hasCurStationName = curStationName != ""
			}
			nextIsTerminal := nextNode.Type == "station" && nextNode.Name == endStation
			if hasCurStationName && repeatsStation(g, cur, curStationName, startStation, endStation, nextIsTerminal) {
				continue
			}
			nextStationName := ""
			if nextNode.Type == "station" {
				nextStationName = nextNode.Name
			}
			if nextStationName != "" && nextStationName != curStationName {
				if repeatsStation(g, cur, nextStationName, startStation, endStation, nextIsTerminal) {
					continue
				}
			}
			if inPath(cur, nextID) {
				closesLoop := nextID == startID && nextNode.Type == "station" && nextNode.Name == endStation
				if !closesLoop {
					continue
				}
			}
			// 与插件 GeoRouteEngine 一致：中途站的处理
			if nextNode.Type == "station" && nextNode.Name != endStation {
				// 存在正线绕行 → 放弃穿越该 station（避免快速车误进停靠线）
				if hasMainlineBypass(g, cur.nodeID, nextID) {
					continue
				}
				// 折返站 → 快速车不穿越（与插件 isReverseStation 跳过一致）
				if nextNode.Name != "" && isReverse != nil && isReverse(link.LineID, nextNode.Name) {
					continue
				}
			}
			pq.push(&entry{nodeID: nextID, dist: cur.dist + link.Distance, link: &link, prev: cur})
		}
	}
	return results
}

// hasMainlineBypass 结构判定某处是否存在「正线绕行」——进站道岔 nodeId 与停靠线车站 stationId
// 连接了同一个出站道岔。复刻插件 GeoRouteEngine.hasMainlineBypass：用坐标比较出站道岔，
// 兼容两线共线但节点 id 不同的情况。
func hasMainlineBypass(g *Graph, nodeID, stationID string) bool {
	stationLinks := g.Links(stationID)
	if len(stationLinks) == 0 {
		return false
	}
	stationOut := g.Nodes[stationLinks[0].To]
	if stationOut == nil {
		return false
	}
	for _, link := range g.Links(nodeID) {
		to := g.Nodes[link.To]
		if to != nil && coordEquals(to, stationOut) {
			return true
		}
	}
	return false
}

func coordEquals(a, b *Node) bool {
	return a.X == b.X && a.Y == b.Y && a.Z == b.Z
}

// enterFaceAllows 入向面门控（复刻插件 GeoRouteEngine.enterFaceAllows）：沿 inLink 到达当前节点后，
// 是否允许接着走 outLink。仅当 outLink 声明了允许到达面集合（enterFrom 非空）、inLink 也带到达面
// （enterTo 非空）、且该到达面不在集合内时才拒绝。任一信息缺失都放行，保证向后兼容与起点正常展开。
func enterFaceAllows(inLink *Link, outLink *Link) bool {
	if inLink == nil {
		return true
	}
	allowed := outLink.EnterFrom
	if len(allowed) == 0 {
		return true
	}
	if inLink.EnterTo == nil {
		return true
	}
	arrivedFace := *inLink.EnterTo
	for _, f := range allowed {
		if f == arrivedFace {
			return true
		}
	}
	return false
}

func inPath(e *entry, nodeID string) bool {
	for cur := e; cur != nil; cur = cur.prev {
		if cur.nodeID == nodeID {
			return true
		}
	}
	return false
}

func repeatsStation(g *Graph, cur *entry, stationName, startStation, endStation string, nextIsTerminal bool) bool {
	if stationName == "" || !stationInPath(g, cur, stationName) {
		return false
	}
	return !nextIsTerminal || stationName != startStation || stationName != endStation
}

func stationInPath(g *Graph, e *entry, stationName string) bool {
	var outgoing *Link
	for cur := e; cur != nil; cur = cur.prev {
		lineID := ""
		if outgoing != nil {
			lineID = outgoing.LineID
		}
		currentStation := getNodeStationName(g, cur.nodeID, lineID)
		if currentStation == stationName {
			return true
		}
		outgoing = cur.link
	}
	return false
}

func getNodeStationName(g *Graph, nodeID, lineID string) string {
	node := g.Nodes[nodeID]
	if node == nil {
		return ""
	}
	if node.Type == "station" {
		return node.Name
	}
	return g.platformNameOfMainlineSwitch(nodeID, lineID)
}

// buildPath 从回溯链构建 RoutePath（与前端结构一致，距离换算为 km）。
func buildPath(g *Graph, end *entry) model.RoutePath {
	var nodeIDs, lineIDSequence, departDirectionSequence []string
	type segMeter struct {
		lineID   string
		meters   float64
		systemID string
	}
	var segMeters []segMeter

	for e := end; e != nil && e.link != nil; e = e.prev {
		nodeIDs = append(nodeIDs, e.nodeID)
		lineIDSequence = append(lineIDSequence, e.link.LineID)
		departDirectionSequence = append(departDirectionSequence, e.link.DepartDir)
		segMeters = append(segMeters, segMeter{lineID: e.link.LineID, meters: e.link.Distance, systemID: e.link.SystemID})
	}
	// 加起点节点
	startEntry := end
	for startEntry.prev != nil {
		startEntry = startEntry.prev
	}
	nodeIDs = append(nodeIDs, startEntry.nodeID)

	reverseStrings(nodeIDs)
	reverseStrings(lineIDSequence)
	reverseStrings(departDirectionSequence)
	for i, j := 0, len(segMeters)-1; i < j; i, j = i+1, j-1 {
		segMeters[i], segMeters[j] = segMeters[j], segMeters[i]
	}

	var stations []string
	var stationSteps []model.StationStep
	for i, id := range nodeIDs {
		n := g.Nodes[id]
		if n != nil && n.Type == "station" && n.Name != "" {
			lineID := ""
			if i < len(lineIDSequence) {
				lineID = lineIDSequence[i]
			} else if i-1 >= 0 && i-1 < len(lineIDSequence) {
				lineID = lineIDSequence[i-1]
			}
			pushStationStep(&stationSteps, n.Name, lineID)
		} else if n != nil {
			lineID := ""
			if i < len(lineIDSequence) {
				lineID = lineIDSequence[i]
			}
			stationName := g.platformNameOfMainlineSwitch(id, lineID)
			if stationName != "" && lineID != "" {
				pushStationStep(&stationSteps, stationName, lineID)
			}
		}
	}
	for _, s := range stationSteps {
		stations = append(stations, s.StationName)
	}

	segments := make([]model.RouteSegment, len(segMeters))
	totalMeters := 0.0
	for i, s := range segMeters {
		segments[i] = model.RouteSegment{LineID: s.lineID, Distance: s.meters / 1000, SystemID: s.systemID}
		totalMeters += s.meters
	}

	return model.RoutePath{
		Stations:                stations,
		StationSteps:            stationSteps,
		NodeIDs:                 nodeIDs,
		LineIDSequence:          lineIDSequence,
		DepartDirectionSequence: departDirectionSequence,
		Distance:                totalMeters / 1000,
		Segments:                segments,
		EstimatedFare:           0, // 票价估算在上层按系统 pricePerKm 计算
	}
}

func pushStationStep(steps *[]model.StationStep, stationName, departLineID string) {
	if len(*steps) == 0 || (*steps)[len(*steps)-1].StationName != stationName {
		*steps = append(*steps, model.StationStep{StationName: stationName, DepartLineID: departLineID})
	}
}

func reverseStrings(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

// RawJourney 是一次换乘行程的原始寻路结果（票价在上层计算）。
type RawJourney struct {
	Legs             []model.RoutePath
	TransferStations []string
	TotalDistance    float64
}

// FindTransferJourneys 启发式寻找「一次换乘」的行程方案（两段直达）：起点站 → 换乘站 → 终点站。
// 复刻插件 GeoRouteEngine.findTransferJourneys。用站名级缩合距离矩阵（Graph.stationDirectDistances）
// 枚举全部换乘站按估计总距离（下界）预筛排序，只对最有潜力的前若干个做两段真实寻路实体化。
//
// maxResults：最多返回方案数（<=0 不限制）。
// minImprovement：最低改善比例 [0,1)：换乘总距离须 < 最短直达 ×(1-此值)；两站无直达时门槛不生效。
func FindTransferJourneys(ctx context.Context, g *Graph, startStation, endStation string, maxResults int, minImprovement float64, isReverse ReversePredicate) []RawJourney {
	if startStation == "" || endStation == "" || startStation == endStation {
		return nil
	}

	// 直达最短距离 → 阈值。无直达则 +∞，任何换乘方案都接受
	directPaths := FindByStation(ctx, g, startStation, endStation, 0, isReverse)
	if ctx.Err() != nil {
		return nil
	}
	bestDirect := infinity
	for _, p := range directPaths {
		if p.Distance < bestDirect {
			bestDirect = p.Distance
		}
	}
	threshold := bestDirect
	if bestDirect < infinity && minImprovement > 0 {
		threshold = bestDirect * (1 - minImprovement)
	}

	// 站名级缩合矩阵：枚举全部换乘站，按「start 直达 mid + mid 直达 end」估计总距离（下界）预筛 + 排序。
	matrix := g.stationDirectDistances()
	fromStart := matrix[startStation]
	type ranked struct {
		mid string
		est float64
	}
	var rankedList []ranked
	for mid, d1 := range fromStart {
		if mid == startStation || mid == endStation {
			continue
		}
		d2, ok := matrix[mid][endStation]
		if !ok {
			continue // mid 到不了终点
		}
		est := d1 + d2
		if est >= threshold {
			continue // 下界都不比阈值近
		}
		rankedList = append(rankedList, ranked{mid: mid, est: est})
	}
	sort.Slice(rankedList, func(i, j int) bool { return rankedList[i].est < rankedList[j].est })

	// 只对最有潜力的前若干候选做实体化；取需要条数的数倍作缓冲，兼顾下界乐观导致的淘汰。<=0（不限）时实体化全部。
	const materializeFactor = 3
	const materializeMin = 8
	materializeCap := infinityInt
	if maxResults > 0 {
		materializeCap = maxResults * materializeFactor
		if materializeCap < materializeMin {
			materializeCap = materializeMin
		}
	}

	// 逐候选站实体化两段真实路径，按换乘站去重（留总距离最短者）
	byTransfer := make(map[string]RawJourney)
	materialized := 0
	for _, r := range rankedList {
		if materialized >= materializeCap {
			break
		}
		if ctx.Err() != nil {
			// ctx 已取消/超时：不再实体化剩余候选站，但已经实体化成功的换乘方案（如果有）仍是
			// 真实完整的方案，继续走下面的排序/截断逻辑返回它们，而不是一并丢弃。
			break
		}
		leg1 := FindByStation(ctx, g, startStation, r.mid, 1, isReverse)
		if len(leg1) == 0 {
			continue
		}
		leg2 := FindByStation(ctx, g, r.mid, endStation, 1, isReverse)
		if len(leg2) == 0 {
			continue
		}
		materialized++
		total := leg1[0].Distance + leg2[0].Distance
		if total >= threshold {
			continue
		}
		if old, ok := byTransfer[r.mid]; !ok || total < old.TotalDistance {
			byTransfer[r.mid] = RawJourney{Legs: []model.RoutePath{leg1[0], leg2[0]}, TransferStations: []string{r.mid}, TotalDistance: total}
		}
	}

	ret := make([]RawJourney, 0, len(byTransfer))
	for _, j := range byTransfer {
		ret = append(ret, j)
	}
	sort.Slice(ret, func(i, j int) bool { return ret[i].TotalDistance < ret[j].TotalDistance })
	if maxResults > 0 && len(ret) > maxResults {
		ret = ret[:maxResults]
	}
	return ret
}

const infinity = 1e18
const infinityInt = int(^uint(0) >> 1)
