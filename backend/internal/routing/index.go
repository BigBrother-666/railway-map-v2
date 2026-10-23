package routing

import "sort"

// searchIndex 是寻路热路径用的整数化只读图索引：节点 / 车站名映射为下标，出边 / 入边为边下标切片，
// 并预计算每条边的 getNodeStationName(From, &link)，让 kShortest 的扩展与路径回溯只做整数比较，
// 不再每步拼字符串查 map。图建好后不再修改，索引可被多个查询并发读取。
type searchIndex struct {
	nodeIdx map[string]int32
	nodeIDs []string
	// exists 标记节点是否真在 Graph.Nodes 中（只作为边端点出现的「幽灵」节点为 false）。
	exists    []bool
	isStation []bool
	// nodeName 是 station 节点的站名 id；非 station 或无名为 -1。
	nodeName []int32
	nameIdx  map[string]int32
	names    []string
	out      [][]int32 // 节点 → 出边下标（保持 Graph.adjacency 原顺序）
	in       [][]int32 // 节点 → 入边下标
	links    []Link
	linkFrom []int32
	linkTo   []int32
	// crossName[l] 是 getNodeStationName(links[l].From, &links[l]) 的站名 id：起点是车站时为其站名，
	// 起点是进站道岔且本边走正线时为被跨越的车站名，否则 -1。
	crossName []int32
}

// index 惰性构建并返回图的寻路索引（并发安全，只构建一次）。
func (g *Graph) index() *searchIndex {
	g.ixOnce.Do(func() { g.ix = buildSearchIndex(g) })
	return g.ix
}

func buildSearchIndex(g *Graph) *searchIndex {
	ix := &searchIndex{nodeIdx: make(map[string]int32), nameIdx: make(map[string]int32)}
	nameID := func(name string) int32 {
		if name == "" {
			return -1
		}
		if id, ok := ix.nameIdx[name]; ok {
			return id
		}
		id := int32(len(ix.names))
		ix.nameIdx[name] = id
		ix.names = append(ix.names, name)
		return id
	}
	nodeID := func(id string) int32 {
		if i, ok := ix.nodeIdx[id]; ok {
			return i
		}
		i := int32(len(ix.nodeIDs))
		ix.nodeIdx[id] = i
		ix.nodeIDs = append(ix.nodeIDs, id)
		n := g.Nodes[id]
		ix.exists = append(ix.exists, n != nil)
		ix.isStation = append(ix.isStation, n != nil && n.Type == "station")
		name := int32(-1)
		if n != nil && n.Type == "station" {
			name = nameID(n.Name)
		}
		ix.nodeName = append(ix.nodeName, name)
		ix.out = append(ix.out, nil)
		ix.in = append(ix.in, nil)
		return i
	}

	// 按 id 排序编号，保证索引确定性（不影响寻路结果，只是便于调试复现）
	ids := make([]string, 0, len(g.Nodes))
	for id := range g.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		nodeID(id)
	}
	froms := make([]string, 0, len(g.adjacency))
	for from := range g.adjacency {
		froms = append(froms, from)
	}
	sort.Strings(froms)

	mainline := g.buildMainlineSwitchStations()
	g.mainlineSwitchCache = mainline
	for _, from := range froms {
		fi := nodeID(from)
		for _, l := range g.adjacency[from] {
			ti := nodeID(l.To)
			li := int32(len(ix.links))
			ix.links = append(ix.links, l)
			ix.linkFrom = append(ix.linkFrom, fi)
			ix.linkTo = append(ix.linkTo, ti)
			cross := int32(-1)
			if ix.isStation[fi] {
				cross = ix.nodeName[fi]
			} else if ix.exists[fi] && !l.IsSiding() && l.LineID != "" {
				cross = nameID(mainline[from+"|"+l.LineID])
			}
			ix.crossName = append(ix.crossName, cross)
			ix.out[fi] = append(ix.out[fi], li)
			ix.in[ti] = append(ix.in[ti], li)
		}
	}
	return ix
}

// idxItem / idxHeap 是按 d 排序的 (下标, 距离) 二叉最小堆，供启发式与缩合矩阵的 Dijkstra 使用。
type idxItem struct {
	i int32
	d float64
}

type idxHeap struct {
	items []idxItem
}

func (h *idxHeap) push(it idxItem) {
	h.items = append(h.items, it)
	i := len(h.items) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if h.items[parent].d <= h.items[i].d {
			break
		}
		h.items[parent], h.items[i] = h.items[i], h.items[parent]
		i = parent
	}
}

func (h *idxHeap) pop() idxItem {
	top := h.items[0]
	last := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	if n := len(h.items); n > 0 {
		h.items[0] = last
		i := 0
		for {
			l, r := 2*i+1, 2*i+2
			s := i
			if l < n && h.items[l].d < h.items[s].d {
				s = l
			}
			if r < n && h.items[r].d < h.items[s].d {
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

func (h *idxHeap) size() int { return len(h.items) }
