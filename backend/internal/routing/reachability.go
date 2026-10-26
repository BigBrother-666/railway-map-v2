package routing

import (
	"context"
	"math"
)

// testKShortestFullSearchHook 是仅供测试用的同步点：kShortest 决定放弃短路、进入完整枚举循环前调用。
// 生产环境恒为 nil。用于测试断言「短路命中时完全不会进入昂贵的枚举循环」。
var testKShortestFullSearchHook func()

// testKShortestResultFoundHook 是仅供测试用的同步点：kShortest 每次找到一条完整路径（append 进
// results）后调用。生产环境恒为 nil。用于测试确定性构造「计算超时发生在已经拿到非空部分结果之后」
// 这一时序（不依赖真实计时窗口猜测）。
var testKShortestResultFoundHook func()

// testReachabilityEntryHook 是仅供测试用的同步点：每次为某个终点站计算 target（启发式 / 可达性）时最先执行。
// 生产环境恒为 nil。用于测试确定性构造「两站真正不可达、但判断过程被计算超时打断」这一时序——
// 不可达的图本身求解极快（比如完全没有边），真实计时窗口很难可靠地在超时前后精确插入判断点，
// 用这个 hook 在搜索真正开始前先 sleep 一段可控时间，让外层的 computeCtx 有机会先过期。
var testReachabilityEntryHook func()

var posInf = math.Inf(1)

// target 是某个终点站名在一次查询内的预处理结果，供 kShortest 做 A* 剪枝。
//
// h[l] 是「沿边 l 到达 l.To 之后」到任一终点站节点的最短剩余距离（米）：套用 kShortest 除
// 「不可重复经过同一节点 / 同名站」之外的全部边约束（入向面门控 / 停靠线中途站跳过 / 折返站跳过 /
// 仅展示线路跳过 / 终点站不再穿越），不可达为 +Inf。状态取「到达所经的边」，因为入向面门控只取决于它的 enterTo。
//
// 忽略重复经过约束只会让剩余距离更短，故 h 是 kShortest 真实剩余距离的下界（可采纳）；又因
// h[l] <= w(l') + h[l'] 对每个合法接续 l→l' 成立（一致），A* 按 g+h 出队时终点仍按 g 非降序出队，
// 找到的 K 条路线与按 g 出队的原始算法一致（仅等长路线之间的先后可能不同）。h=+Inf 的边永远到不了
// 终点，直接剪掉——这同时取代了原来「先跑一遍可达性 Dijkstra」的短路判断。
type target struct {
	endName int32 // 终点站名 id；图中没有该站名时为 -1
	h       []float64
}

func (t *target) isEnd(ix *searchIndex, node int32) bool {
	return t.endName >= 0 && ix.isStation[node] && ix.nodeName[node] == t.endName
}

// startH 是从 start 节点（尚未经过任何边，入向面门控对第一跳恒放行）出发的剩余距离下界。
func (t *target) startH(ix *searchIndex, start int32) float64 {
	best := posInf
	for _, li := range ix.out[start] {
		if d := ix.links[li].Distance + t.h[li]; d < best {
			best = d
		}
	}
	return best
}

// computeTarget 在边状态图上从全部终点站反向跑 Dijkstra 得到 h。ctx 取消时返回 nil。
func computeTarget(ctx context.Context, ix *searchIndex, endStation string, isReverse ReversePredicate, isDisplayOnly LinePredicate) *target {
	if testReachabilityEntryHook != nil {
		testReachabilityEntryHook()
	}
	t := &target{endName: -1, h: make([]float64, len(ix.links))}
	if id, ok := ix.nameIdx[endStation]; ok {
		t.endName = id
	}
	// blocked：kShortest 必然剪掉的边——目标节点不存在、属于仅展示线路，或是「非终点」中途车站且经停靠线进入 / 属于折返站。
	blocked := make([]bool, len(ix.links))
	h := &idxHeap{}
	for li := range ix.links {
		t.h[li] = posInf
		to := ix.linkTo[li]
		if !ix.exists[to] || (isDisplayOnly != nil && isDisplayOnly(ix.links[li].LineID)) {
			blocked[li] = true
			continue
		}
		if t.isEnd(ix, to) {
			t.h[li] = 0
			h.push(idxItem{i: int32(li), d: 0})
			continue
		}
		if ix.isStation[to] {
			link := &ix.links[li]
			if link.IsSiding() {
				blocked[li] = true
			} else if name := ix.nodeName[to]; name >= 0 && isReverse != nil && isReverse(link.LineID, ix.names[name]) {
				blocked[li] = true
			}
		}
	}

	pops := 0
	for h.size() > 0 {
		if pops%4096 == 0 && ctx.Err() != nil {
			return nil
		}
		cur := h.pop()
		pops++
		if cur.d > t.h[cur.i] {
			continue // 过期条目
		}
		u := ix.linkFrom[cur.i]
		if t.isEnd(ix, u) {
			continue // 到达终点即停止，不会从终点站接着走 cur.i
		}
		next := &ix.links[cur.i]
		nd := cur.d + next.Distance
		for _, pi := range ix.in[u] {
			if blocked[pi] || nd >= t.h[pi] {
				continue
			}
			if !enterFaceAllows(&ix.links[pi], next) {
				continue
			}
			t.h[pi] = nd
			h.push(idxItem{i: pi, d: nd})
		}
	}
	return t
}

// reachableIgnoringRevisit 判断从 startID 到任一名为 endStation 的车站节点是否存在满足 kShortest
// 除「同名站不可重复经过」之外全部边约束的路径（至少走过一条边）。返回 false 时 kShortest 在完整约束下
// 也必然无解。kShortest 内部直接用 target.startH 判断，这里是同一口径的独立入口。
func reachableIgnoringRevisit(ctx context.Context, g *Graph, startID, endStation string, isReverse ReversePredicate) bool {
	ix := g.index()
	start, ok := ix.nodeIdx[startID]
	if !ok || !ix.exists[start] {
		return false
	}
	t := computeTarget(ctx, ix, endStation, isReverse, nil)
	if t == nil {
		return false
	}
	return !math.IsInf(t.startH(ix, start), 1)
}
