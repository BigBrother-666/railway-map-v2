package routing

import (
	"context"
	"math"
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

// searcher 是一次查询内共享的寻路上下文：整数化图索引 + 按终点站名缓存的 A* 启发式（target）。
// 同一次查询里多次以同一站为终点寻路（如联程票的所有第二段都以终点站为终点）时，启发式只算一次。
// 不并发安全，每次查询各建一个。
type searcher struct {
	ctx       context.Context
	g         *Graph
	ix        *searchIndex
	isReverse ReversePredicate
	targets   map[string]*target
	// ksp 缓存 (起点节点, 终点站名) 的 kShortest 结果。kShortest 是确定性的，且只在出队前检查条数，
	// 故 k 条的结果恰是更大 k 结果的前缀：已算过更大 k、或已穷尽（结果不足 k 条且未被取消）时直接取前缀。
	ksp map[kspKey]kspResult
}

type kspKey struct {
	start int32
	end   string
}

type kspResult struct {
	paths     []model.RoutePath
	k         int
	exhausted bool // 结果少于 k 条且未被 ctx 打断：再大的 k 也不会多出结果
}

func newSearcher(ctx context.Context, g *Graph, isReverse ReversePredicate) *searcher {
	return &searcher{ctx: ctx, g: g, ix: g.index(), isReverse: isReverse, targets: make(map[string]*target), ksp: make(map[kspKey]kspResult)}
}

// kShortestCached 是带查询内缓存的 kShortest，返回的切片只读。
func (s *searcher) kShortestCached(startID, endStation string, k int) []model.RoutePath {
	start, ok := s.ix.nodeIdx[startID]
	if !ok {
		return nil
	}
	key := kspKey{start: start, end: endStation}
	if c, ok := s.ksp[key]; ok && (c.k >= k || c.exhausted) {
		if len(c.paths) > k {
			return c.paths[:k]
		}
		return c.paths
	}
	paths := s.kShortest(startID, endStation, k)
	if s.ctx.Err() == nil {
		s.ksp[key] = kspResult{paths: paths, k: k, exhausted: len(paths) < k}
		return paths
	}
	// 被 ctx 打断：已找到的几条仍是完整结果的前缀，按实际条数缓存（不标记穷尽），
	// 之后同一查询里再要不超过这么多条时直接复用，不会因为 ctx 已过期而一条都拿不到。
	if c, ok := s.ksp[key]; ok && len(c.paths) >= len(paths) {
		if len(c.paths) > k {
			return c.paths[:k]
		}
		return c.paths
	}
	s.ksp[key] = kspResult{paths: paths, k: len(paths)}
	return paths
}

// target 返回终点站名的启发式，ctx 已取消时返回 nil（不缓存）。
func (s *searcher) target(endStation string) *target {
	if t, ok := s.targets[endStation]; ok {
		return t
	}
	t := computeTarget(s.ctx, s.ix, endStation, s.isReverse)
	if t != nil {
		s.targets[endStation] = t
	}
	return t
}

// entry 是 kShortest 搜索树上的一个节点（一条从起点出发的部分路径的末端）。
type entry struct {
	node int32
	link int32   // 到达本节点所经的边下标；起点为 -1
	g    float64 // 已走距离（米）
	f    float64 // g + 启发式剩余距离下界，优先队列按此排序
	// transfers 是已走路线的转线次数（相邻两段 lineId 不同的次数）。f 相同时转线少者先出队：
	// 平行轨道上常有多条等长路线，先出队的会被选作该站台的结果，与 isBetterRoute「转线少者优先」一致。
	transfers int32
	prev      *entry
	// nodeMask / nameMask 是路径上节点 / 已确定站名（严格祖先贡献的 crossName）的 64 位布隆过滤器：
	// 对应位未置位即可 O(1) 判定「不在路径上」，置位时才回溯精确确认。
	nodeMask uint64
	nameMask uint64
}

func bloomBit(i int32) uint64 { return 1 << (uint32(i) & 63) }

// entryArena 按块分配 entry，减少海量小对象分配与 GC 压力。
type entryArena struct {
	chunk []entry
}

func (a *entryArena) alloc() *entry {
	if len(a.chunk) == cap(a.chunk) {
		a.chunk = make([]entry, 0, 4096)
	}
	a.chunk = a.chunk[:len(a.chunk)+1]
	return &a.chunk[len(a.chunk)-1]
}

// minHeap 是按 (f, transfers) 排序的二叉最小堆：push/pop 均 O(log n)。
type minHeap struct {
	items []*entry
}

// fTieEpsilon（米）：f = g + h 中 h 是反向累加的，同一长度的两条路线算出的 f 可能差几个 ulp，
// 差值小于此值视为等长，再按转线次数决胜。
const fTieEpsilon = 1e-6

func entryLess(a, b *entry) bool {
	if d := a.f - b.f; d < -fTieEpsilon || d > fTieEpsilon {
		return d < 0
	}
	if a.transfers != b.transfers {
		return a.transfers < b.transfers
	}
	return a.f < b.f
}

func (h *minHeap) push(e *entry) {
	h.items = append(h.items, e)
	i := len(h.items) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if !entryLess(h.items[i], h.items[parent]) {
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
	if n := len(h.items); n > 0 {
		h.items[0] = last
		i := 0
		for {
			l, r := 2*i+1, 2*i+2
			s := i
			if l < n && entryLess(h.items[l], h.items[s]) {
				s = l
			}
			if r < n && entryLess(h.items[r], h.items[s]) {
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
	return newSearcher(ctx, g, isReverse).findByStation(startStation, endStation, maxResults)
}

func (s *searcher) findByStation(startStation, endStation string, maxResults int) []model.RoutePath {
	kPerPlatform := maxResults
	if kPerPlatform <= 0 {
		kPerPlatform = kspSafetyCap
	}
	var all []model.RoutePath
	for _, startID := range s.g.StationNodes(startStation) {
		all = append(all, s.kShortestCached(startID, endStation, kPerPlatform)...)
		if s.ctx.Err() != nil {
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
func kShortest(ctx context.Context, g *Graph, startID, endStation string, k int, isReverse ReversePredicate) []model.RoutePath {
	return newSearcher(ctx, g, isReverse).kShortest(startID, endStation, k)
}

// kShortest 按「已走距离 + 剩余距离下界」（A*，见 target）扩展部分路径，跳过已在当前路径前缀中的节点
// 保证无环（允许回到起点闭合环线），同名站不可重复经过。启发式可采纳，终点按真实距离非降序出队，
// 找到的 K 条路线与按已走距离扩展的朴素枚举一致；剩余距离为 +Inf（到不了终点）的分支直接剪掉，
// 两站根本无解时一条都不展开。
func (s *searcher) kShortest(startID, endStation string, k int) []model.RoutePath {
	ix := s.ix
	start, ok := ix.nodeIdx[startID]
	if !ok || !ix.exists[start] || k < 1 {
		return nil
	}
	t := s.target(endStation)
	if t == nil {
		return nil
	}
	startH := t.startH(ix, start)
	if math.IsInf(startH, 1) {
		return nil
	}
	if testKShortestFullSearchHook != nil {
		testKShortestFullSearchHook()
	}
	startName := ix.nodeName[start] // 起点站名 id（起点非车站时为 -1）

	// repeats 复刻原 repeatsStation：name 已在路径上出现过，且不是「回到同名起点站闭合环线」这一特例。
	repeats := func(cur *entry, name int32, nextIsTerminal bool) bool {
		if !stationInPath(ix, cur, name) {
			return false
		}
		return !nextIsTerminal || name != startName || name != t.endName
	}

	var results []model.RoutePath
	arena := &entryArena{}
	root := arena.alloc()
	*root = entry{node: start, link: -1, f: startH, nodeMask: bloomBit(start)}
	pq := &minHeap{}
	pq.push(root)
	pops := 0

	for pq.size() > 0 && len(results) < k && pops < maxPops {
		if pops%4096 == 0 && s.ctx.Err() != nil {
			return results
		}
		cur := pq.pop()
		pops++

		u := cur.node
		if cur.link >= 0 && t.isEnd(ix, u) {
			results = append(results, buildPath(s.g, ix, cur))
			if testKShortestResultFoundHook != nil {
				testKShortestResultFoundHook()
			}
			continue
		}
		var inLink *Link
		if cur.link >= 0 {
			inLink = &ix.links[cur.link]
		}
		uIsSwitch := !ix.isStation[u]
		for _, li := range ix.out[u] {
			// h=+Inf 已涵盖：目标节点不存在、经停靠线进入中途站、折返站，以及之后无论如何到不了终点
			h := t.h[li]
			if math.IsInf(h, 1) {
				continue
			}
			link := &ix.links[li]
			// 入向面门控：与插件 enterFaceAllows 一致，拒绝「从错误到达面接反向牌出边」的非法接续
			if !enterFaceAllows(inLink, link) {
				continue
			}
			v := ix.linkTo[li]
			nextIsTerminal := t.isEnd(ix, v)
			curName := int32(-1)
			if uIsSwitch {
				curName = ix.crossName[li] // 进站道岔走正线跨越的车站
			}
			if curName >= 0 && repeats(cur, curName, nextIsTerminal) {
				continue
			}
			if nextName := ix.nodeName[v]; nextName >= 0 && nextName != curName {
				if repeats(cur, nextName, nextIsTerminal) {
					continue
				}
			}
			if inPath(cur, v) {
				closesLoop := v == start && nextIsTerminal
				if !closesLoop {
					continue
				}
			}
			next := arena.alloc()
			g := cur.g + link.Distance
			transfers := cur.transfers
			if inLink != nil && inLink.LineID != link.LineID {
				transfers++
			}
			*next = entry{
				node: v, link: li, g: g, f: g + h, transfers: transfers, prev: cur,
				nodeMask: cur.nodeMask | bloomBit(v),
				nameMask: cur.nameMask | crossBit(ix.crossName[li]),
			}
			pq.push(next)
		}
	}
	return results
}

func crossBit(name int32) uint64 {
	if name < 0 {
		return 0
	}
	return bloomBit(name)
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

// inPath 判断节点是否已在 e 所在的路径上（含起点）。
func inPath(e *entry, node int32) bool {
	if e.nodeMask&bloomBit(node) == 0 {
		return false
	}
	for cur := e; cur != nil; cur = cur.prev {
		if cur.node == node {
			return true
		}
	}
	return false
}

// stationInPath 复刻插件 stationInPath：路径上各节点按「车站节点取站名、进站道岔沿下一条出边走正线取被跨越的
// 站名」（getNodeStationName）后是否出现过 name。末端节点 e 尚无出边，只计其自身站名。
func stationInPath(ix *searchIndex, e *entry, name int32) bool {
	if ix.nodeName[e.node] == name {
		return true
	}
	if e.nameMask&bloomBit(name) == 0 {
		return false
	}
	for cur := e; cur.prev != nil; cur = cur.prev {
		if ix.crossName[cur.link] == name {
			return true
		}
	}
	return false
}

// buildPath 从回溯链构建 RoutePath（与前端结构一致，距离换算为 km）。
func buildPath(g *Graph, ix *searchIndex, end *entry) model.RoutePath {
	var chain []*entry
	for e := end; e != nil; e = e.prev {
		chain = append(chain, e)
	}
	// chain 从终点到起点，翻转成起点在前
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	nodeIDs := make([]string, len(chain))
	lineIDSequence := make([]string, 0, len(chain)-1)
	departDirectionSequence := make([]string, 0, len(chain)-1)
	segments := make([]model.RouteSegment, 0, len(chain)-1)
	totalMeters := 0.0
	for i, e := range chain {
		nodeIDs[i] = ix.nodeIDs[e.node]
		if e.link < 0 {
			continue
		}
		l := &ix.links[e.link]
		lineIDSequence = append(lineIDSequence, l.LineID)
		departDirectionSequence = append(departDirectionSequence, l.DepartDir)
		segments = append(segments, model.RouteSegment{LineID: l.LineID, Distance: l.Distance / 1000, SystemID: l.SystemID})
		totalMeters += l.Distance
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

// RawJourney 是一次换乘行程的原始寻路结果（票价在上层计算）。
type RawJourney struct {
	Legs []model.RoutePath
	// TransferStations[i] 是 Legs[i] 的终点站名；Legs[i+1] 的起点站可能是与之同站组的另一个车站
	// （站组内步行换乘），其站名以 Legs[i+1] 自身为准。
	TransferStations []string
	TotalDistance    float64
}

// GroupMembersFunc 返回某车站所在站组的全部站名（含自身）；不属于任何站组时返回 nil 或仅含自身。
type GroupMembersFunc func(stationName string) []string

// FindTransferJourneys 启发式寻找「一次换乘」的行程方案（两段直达）：起点站 → 换乘站 → 终点站。
// 复刻插件 GeoRouteEngine.findTransferJourneys。用站名级缩合距离矩阵（Graph.stationDirectDistances）
// 枚举全部换乘站按估计总距离（下界）预筛排序，只对最有潜力的前若干个做两段真实寻路实体化。
// 支持站组换乘：首段到达 mid1 后，可从与 mid1 同站组的任一车站 mid2 出发乘第二段（mid2 可等于 mid1）。
//
// maxResults：最多返回方案数（<=0 不限制）。
// minImprovement：最低改善比例 [0,1)：换乘总距离须 < 最短直达 ×(1-此值)；两站无直达时门槛不生效。
// groupMembers：站组查询函数，为 nil 时视为没有任何站组（只能同站换乘）。
func FindTransferJourneys(ctx context.Context, g *Graph, startStation, endStation string, maxResults int, minImprovement float64, isReverse ReversePredicate, groupMembers GroupMembersFunc) []RawJourney {
	return newSearcher(ctx, g, isReverse).findTransferJourneys(startStation, endStation, maxResults, minImprovement, groupMembers)
}

func (s *searcher) findTransferJourneys(startStation, endStation string, maxResults int, minImprovement float64, groupMembers GroupMembersFunc) []RawJourney {
	if startStation == "" || endStation == "" || startStation == endStation {
		return nil
	}

	// 直达最短距离 → 阈值。无直达则 +∞，任何换乘方案都接受
	directPaths := s.findByStation(startStation, endStation, 0)
	if s.ctx.Err() != nil {
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

	// 站名级缩合矩阵：枚举全部 (下车站 mid1, 上车站 mid2) 组合，按「start 直达 mid1 + mid2 直达 end」
	// 估计总距离（下界）预筛 + 排序。mid2 取 mid1 所在站组的成员（含 mid1 自身）。
	matrix := s.g.stationDirectDistances()
	fromStart := matrix[startStation]
	type ranked struct {
		mid1 string
		mid2 string
		est  float64
	}
	var rankedList []ranked
	for mid1, d1 := range fromStart {
		if mid1 == startStation || mid1 == endStation {
			continue
		}
		for _, mid2 := range transferBoardStations(mid1, groupMembers) {
			if mid2 == startStation || mid2 == endStation {
				continue
			}
			d2, ok := matrix[mid2][endStation]
			if !ok {
				continue // mid2 到不了终点
			}
			est := d1 + d2
			if est >= threshold {
				continue // 下界都不比阈值近
			}
			rankedList = append(rankedList, ranked{mid1: mid1, mid2: mid2, est: est})
		}
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

	// 逐候选实体化两段真实路径，按 (mid1, mid2) 去重（留总距离最短者）。
	// 首段只取决于 mid1、次段只取决于 mid2，分别记忆化，避免同一车站出现在多个候选里时重复寻路；
	// 所有次段终点相同，A* 启发式由 searcher 按终点缓存只算一次。
	leg1Memo := make(map[string][]model.RoutePath)
	leg2Memo := make(map[string][]model.RoutePath)
	byTransfer := make(map[string]RawJourney)
	// kept 是已接受方案的总距离（升序），用于分支定界提前结束。
	var kept []float64
	materialized := 0
	for _, r := range rankedList {
		if materialized >= materializeCap {
			break
		}
		// 分支定界：候选按下界升序，已凑够 maxResults 个方案且第 maxResults 名不比本候选下界差时，
		// 后面的候选真实总距离都 >= 下界，不可能挤进前 maxResults，结果与继续实体化到上限相同。
		if maxResults > 0 && len(kept) >= maxResults && kept[maxResults-1] <= r.est {
			break
		}
		if s.ctx.Err() != nil {
			// ctx 已取消/超时：不再实体化剩余候选站，但已经实体化成功的换乘方案（如果有）仍是
			// 真实完整的方案，继续走下面的排序/截断逻辑返回它们，而不是一并丢弃。
			break
		}
		leg1, ok := leg1Memo[r.mid1]
		if !ok {
			leg1 = s.findByStation(startStation, r.mid1, 1)
			leg1Memo[r.mid1] = leg1
		}
		if len(leg1) == 0 {
			continue
		}
		leg2, ok := leg2Memo[r.mid2]
		if !ok {
			leg2 = s.findByStation(r.mid2, endStation, 1)
			leg2Memo[r.mid2] = leg2
		}
		if len(leg2) == 0 {
			continue
		}
		materialized++
		total := leg1[0].Distance + leg2[0].Distance
		if total >= threshold {
			continue
		}
		key := r.mid1 + "\x00" + r.mid2
		if old, ok := byTransfer[key]; !ok || total < old.TotalDistance {
			byTransfer[key] = RawJourney{Legs: []model.RoutePath{leg1[0], leg2[0]}, TransferStations: []string{r.mid1}, TotalDistance: total}
			i := sort.SearchFloat64s(kept, total)
			kept = append(kept, 0)
			copy(kept[i+1:], kept[i:])
			kept[i] = total
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

// transferBoardStations 返回在 mid1 下车后可以上车的车站列表：mid1 所在站组的全部成员（保证含 mid1 自身）；
// 无站组时仅返回 [mid1]。
func transferBoardStations(mid1 string, groupMembers GroupMembersFunc) []string {
	if groupMembers == nil {
		return []string{mid1}
	}
	members := groupMembers(mid1)
	if len(members) == 0 {
		return []string{mid1}
	}
	for _, m := range members {
		if m == mid1 {
			return members
		}
	}
	return append([]string{mid1}, members...)
}

const infinity = 1e18
const infinityInt = int(^uint(0) >> 1)
