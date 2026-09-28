package routing

import "context"

// reachMaxPops 是 reachableIgnoringRevisit 的出队安全阀。正常图上状态空间（节点数 × 到达面变体数）
// 远小于此值，只是防御异常输入导致的无限循环。
const reachMaxPops = 500_000

// reachEntry 是可达性 Dijkstra 的一个队列条目：状态 = (节点, 到达面)。
type reachEntry struct {
	nodeID string
	face   *string
	dist   float64
}

// reachHeap 是按 dist 排序的二叉最小堆。结构与 pathfind.go 的 minHeap 相同但条目类型不同，独立实现
// 以避免与 kShortest 现有热路径共享代码，缩小这次改动对现有正确性的影响面。
type reachHeap struct {
	items []reachEntry
}

func (h *reachHeap) push(e reachEntry) {
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

func (h *reachHeap) pop() reachEntry {
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

func (h *reachHeap) size() int { return len(h.items) }

// testKShortestFullSearchHook 是仅供测试用的同步点：kShortest 决定放弃短路、进入完整枚举循环前调用。
// 生产环境恒为 nil。用于测试断言「短路命中时完全不会进入昂贵的枚举循环」。
var testKShortestFullSearchHook func()

// testKShortestResultFoundHook 是仅供测试用的同步点：kShortest 每次找到一条完整路径（append 进
// results）后调用。生产环境恒为 nil。用于测试确定性构造「计算超时发生在已经拿到非空部分结果之后」
// 这一时序（不依赖真实计时窗口猜测）。
var testKShortestResultFoundHook func()

// testReachabilityEntryHook 是仅供测试用的同步点：reachableIgnoringRevisit 每次被调用时最先执行。
// 生产环境恒为 nil。用于测试确定性构造「两站真正不可达、但判断过程被计算超时打断」这一时序——
// 不可达的图本身求解极快（比如完全没有边），真实计时窗口很难可靠地在超时前后精确插入判断点，
// 用这个 hook 在搜索真正开始前先 sleep 一段可控时间，让外层的 computeCtx 有机会先过期。
var testReachabilityEntryHook func()

// reachableIgnoringRevisit 用标准 Dijkstra（状态 = 节点 + 到达面，每个状态只处理一次）判断从 startID
// 到任一名为 endStation 的车站节点是否存在合法路径，套用 kShortest 除「同名站不可重复经过」之外的
// 全部边约束（enterFaceAllows / hasMainlineBypass / 折返站跳过）。
//
// 忽略「同名站不可重复经过」这一条约束只会让判定更宽松（更容易判定为「可达」），绝不会把真实可达的
// 两点误判为不可达——因此这里返回 false 时，kShortest 在完整约束下也必然找不到任何合法路径，可安全
// 用作必要条件剪枝：不可达时让 kShortest 跳过昂贵的完整路径枚举直接返回空；可达时不影响任何现有行为
// （kShortest 仍会按原逻辑跑一遍，因为可能恰好都被同名站约束挡住，这里的「可达」只是必要条件不是充分条件）。
func reachableIgnoringRevisit(ctx context.Context, g *Graph, startID, endStation string, isReverse ReversePredicate) bool {
	if testReachabilityEntryHook != nil {
		testReachabilityEntryHook()
	}
	startNode := g.Nodes[startID]
	if startNode == nil {
		return false
	}

	dist := make(map[string]float64)
	h := &reachHeap{}

	pushIfBetter := func(nodeID string, face *string, nd float64) {
		key := stateKey(nodeID, face)
		if old, ok := dist[key]; !ok || nd < old {
			dist[key] = nd
			h.push(reachEntry{nodeID: nodeID, face: face, dist: nd})
		}
	}

	// 起点第一跳：等价于 kShortest 里 cur.link==nil 的种子条目，enterFaceAllows(nil, outLink) 恒放行。
	// 故意不把起点本身当作一个普通状态放进 dist——否则「绕回起点、到达面恰好也是无信息」的合法状态会
	// 与起点撞上同一个 key，被误判为已访问过而漏判可达（起点从未真正「以到达面 X 被访问」过，它是查询
	// 的种子，不是搜索过程中被发现的状态）。
	for _, link := range g.Links(startID) {
		if reachSkipIntermediate(g, startID, link, endStation, isReverse) {
			continue
		}
		pushIfBetter(link.To, link.EnterTo, link.Distance)
	}

	pops := 0
	for h.size() > 0 {
		if pops%4096 == 0 && ctx.Err() != nil {
			return false // 已取消：调用方不会使用后续结果，保守返回，避免白跑
		}
		if pops >= reachMaxPops {
			return true // 状态空间异常大：放弃短路判断，交给 kShortest 走完整逻辑（不产生假阴性）
		}
		cur := h.pop()
		pops++
		if best, ok := dist[stateKey(cur.nodeID, cur.face)]; ok && cur.dist > best {
			continue // 过期条目（已被更优路径取代）
		}
		node := g.Nodes[cur.nodeID]
		if node != nil && node.Type == "station" && node.Name == endStation {
			return true
		}
		for _, link := range g.Links(cur.nodeID) {
			if !enterFaceAllowsByFace(cur.face, &link) {
				continue
			}
			if reachSkipIntermediate(g, cur.nodeID, link, endStation, isReverse) {
				continue
			}
			pushIfBetter(link.To, link.EnterTo, cur.dist+link.Distance)
		}
	}
	return false
}

// stateKey 把 (节点, 到达面) 编码成 map key；face 为 nil（无到达面信息）用 NUL 字节做哨兵，
// geojson 里的真实 face 字符串不会包含 NUL，故不会与真实值冲突。
func stateKey(nodeID string, face *string) string {
	if face == nil {
		return nodeID + "|\x00"
	}
	return nodeID + "|" + *face
}

// reachSkipIntermediate 判断沿 link 从 fromID 走到下一节点是否应被过滤——目标节点不存在，或它是一个
// 「非终点」中途车站且存在正线绕行 / 属于折返站（与 kShortest 里同一段过滤逻辑一致）。
func reachSkipIntermediate(g *Graph, fromID string, link Link, endStation string, isReverse ReversePredicate) bool {
	nextNode := g.Nodes[link.To]
	if nextNode == nil {
		return true
	}
	if nextNode.Type == "station" && nextNode.Name != endStation {
		if hasMainlineBypass(g, fromID, link.To) {
			return true
		}
		if nextNode.Name != "" && isReverse != nil && isReverse(link.LineID, nextNode.Name) {
			return true
		}
	}
	return false
}

// enterFaceAllowsByFace 与 enterFaceAllows 语义相同，只是入参是「到达面」而非完整 inLink
// （reachableIgnoringRevisit 的搜索状态只保留到达面，不保留完整 Link）。
func enterFaceAllowsByFace(arrivedFace *string, outLink *Link) bool {
	allowed := outLink.EnterFrom
	if len(allowed) == 0 {
		return true
	}
	if arrivedFace == nil {
		return true
	}
	for _, f := range allowed {
		if f == *arrivedFace {
			return true
		}
	}
	return false
}
