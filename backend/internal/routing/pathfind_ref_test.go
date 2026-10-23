package routing

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"railway-map-backend/internal/model"
)

// 本文件保留优化前的朴素 kShortest（按已走距离出队、字符串回溯判重）作为参考实现，在随机图上
// 与 A* + 整数索引版本逐条对比结果，防止剪枝 / 预计算改坏语义。

type refEntry struct {
	nodeID string
	dist   float64
	link   *Link
	prev   *refEntry
}

type refPath struct {
	ids  []string
	dist float64
}

func refKShortest(g *Graph, startID, endStation string, k int, isReverse ReversePredicate) []refPath {
	var results []refPath
	startNode := g.Nodes[startID]
	if startNode == nil || k < 1 {
		return results
	}
	var startStation string
	if startNode.Type == "station" {
		startStation = startNode.Name
	}
	refName := func(nodeID string, outgoing *Link) string {
		node := g.Nodes[nodeID]
		if node == nil {
			return ""
		}
		if node.Type == "station" {
			return node.Name
		}
		if outgoing == nil || outgoing.IsSiding() {
			return ""
		}
		return g.platformNameOfMainlineSwitch(nodeID, outgoing.LineID)
	}
	stationIn := func(e *refEntry, name string) bool {
		var outgoing *Link
		for cur := e; cur != nil; cur = cur.prev {
			if refName(cur.nodeID, outgoing) == name {
				return true
			}
			outgoing = cur.link
		}
		return false
	}
	repeats := func(cur *refEntry, name string, nextIsTerminal bool) bool {
		if name == "" || !stationIn(cur, name) {
			return false
		}
		return !nextIsTerminal || name != startStation || name != endStation
	}
	inP := func(e *refEntry, id string) bool {
		for cur := e; cur != nil; cur = cur.prev {
			if cur.nodeID == id {
				return true
			}
		}
		return false
	}

	// 参考实现用简单的线性扫描优先队列（取最小 dist，相同取最早入队），图很小无所谓性能
	queue := []*refEntry{{nodeID: startID}}
	pops := 0
	for len(queue) > 0 && len(results) < k && pops < 20000 {
		mi := 0
		for i := 1; i < len(queue); i++ {
			if queue[i].dist < queue[mi].dist {
				mi = i
			}
		}
		cur := queue[mi]
		queue = append(queue[:mi], queue[mi+1:]...)
		pops++
		curNode := g.Nodes[cur.nodeID]
		if curNode != nil && curNode.Type == "station" && curNode.Name == endStation && cur.link != nil {
			var ids []string
			for e := cur; e != nil; e = e.prev {
				ids = append([]string{e.nodeID}, ids...)
			}
			results = append(results, refPath{ids: ids, dist: cur.dist})
			continue
		}
		for _, link := range g.Links(cur.nodeID) {
			link := link
			nextNode := g.Nodes[link.To]
			if nextNode == nil || !enterFaceAllows(cur.link, &link) {
				continue
			}
			curName := ""
			if curNode != nil && curNode.Type != "station" {
				curName = refName(cur.nodeID, &link)
			}
			nextIsTerminal := nextNode.Type == "station" && nextNode.Name == endStation
			if curName != "" && repeats(cur, curName, nextIsTerminal) {
				continue
			}
			nextName := ""
			if nextNode.Type == "station" {
				nextName = nextNode.Name
			}
			if nextName != "" && nextName != curName && repeats(cur, nextName, nextIsTerminal) {
				continue
			}
			if inP(cur, link.To) && !(link.To == startID && nextIsTerminal) {
				continue
			}
			if nextNode.Type == "station" && nextNode.Name != endStation {
				if link.IsSiding() {
					continue
				}
				if nextNode.Name != "" && isReverse != nil && isReverse(link.LineID, nextNode.Name) {
					continue
				}
			}
			queue = append(queue, &refEntry{nodeID: link.To, dist: cur.dist + link.Distance, link: &link, prev: cur})
		}
	}
	return results
}

// randomGraph 生成带道岔、正线 / 停靠线、入向面门控、同名多站台的随机小图。边长取整数，
// 制造大量等长路线以覆盖平局情况。
func randomGraph(t *testing.T, r *rand.Rand) (*Graph, []string) {
	names := []string{"A", "B", "C", "D", "E", "F"}
	var features []model.Feature
	var nodeIDs []string
	nStations := 4 + r.Intn(5)
	for i := 0; i < nStations; i++ {
		id := fmt.Sprintf("s%d", i)
		features = append(features, point(id, "station", names[r.Intn(len(names))], float64(i), 0))
		nodeIDs = append(nodeIDs, id)
	}
	nSwitches := 3 + r.Intn(6)
	for i := 0; i < nSwitches; i++ {
		id := fmt.Sprintf("w%d", i)
		features = append(features, point(id, "switch", "", float64(i), 1))
		nodeIDs = append(nodeIDs, id)
	}
	faces := []string{"0_1", "1_0", "2_3"}
	lines := []string{"L1", "L2"}
	nEdges := len(nodeIDs) + r.Intn(len(nodeIDs)*2)
	for i := 0; i < nEdges; i++ {
		from := nodeIDs[r.Intn(len(nodeIDs))]
		to := nodeIDs[r.Intn(len(nodeIDs))]
		if r.Intn(12) == 0 {
			to = "ghost" // 指向不存在的节点
		}
		if from == to {
			continue
		}
		extra := map[string]any{"departDir": []string{"e", "w", "n", "s"}[r.Intn(4)]}
		switch r.Intn(4) {
		case 0:
			extra["track"] = "siding"
		case 1:
			extra["track"] = "main"
		}
		if r.Intn(3) == 0 {
			extra["enterTo"] = faces[r.Intn(len(faces))]
		}
		if r.Intn(3) == 0 {
			extra["enterFrom"] = []string{faces[r.Intn(len(faces))]}
		}
		lineID := lines[r.Intn(len(lines))]
		features = append(features, line(fmt.Sprintf("e%d", i), from, to, lineID, float64(1+r.Intn(4)), extra))
	}
	return buildGraph(t, features), names
}

func TestKShortest_MatchesReferenceOnRandomGraphs(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	reverse := func(lineID, name string) bool { return lineID == "L2" && name == "C" }
	for iter := 0; iter < 400; iter++ {
		g, names := randomGraph(t, r)
		for _, startName := range names {
			for _, startID := range g.StationNodes(startName) {
				for _, end := range names {
					for _, k := range []int{1, 3, 16} {
						want := refKShortest(g, startID, end, k, reverse)
						got := kShortest(context.Background(), g, startID, end, k, reverse)
						if !samePathMultiset(want, got) {
							t.Fatalf("iter %d %s->%s k=%d\nwant %v\ngot  %v", iter, startID, end, k, want, pathIDs(got))
						}
					}
				}
			}
		}
	}
}

// samePathMultiset 比较两组路线：距离序列必须逐条相同；同距离的路线之间允许顺序不同，非最后一个距离档的
// 节点序列多重集必须一致（平行边会产生节点序列相同的不同路线，故按多重集计数）。最后一个距离档可能被 k
// 截断，其中具体取到哪几条依赖等长路线的出队顺序，只校验条数。
func samePathMultiset(want []refPath, got []model.RoutePath) bool {
	if len(want) != len(got) {
		return false
	}
	wantD := make([]float64, len(want))
	for i, p := range want {
		wantD[i] = p.dist
	}
	for i, p := range got {
		if d := p.Distance * 1000; d < wantD[i]-1e-6 || d > wantD[i]+1e-6 {
			return false
		}
	}
	if len(want) == 0 {
		return true
	}
	last := wantD[len(wantD)-1]
	count := map[string]int{}
	for i, p := range want {
		if wantD[i] != last {
			count[strings.Join(p.ids, ",")]++
		}
	}
	for _, p := range got {
		if p.Distance*1000 < last-1e-6 {
			count[strings.Join(p.NodeIDs, ",")]--
		}
	}
	for _, c := range count {
		if c != 0 {
			return false
		}
	}
	return true
}

func pathIDs(ps []model.RoutePath) [][]string {
	out := make([][]string, len(ps))
	for i, p := range ps {
		out[i] = p.NodeIDs
	}
	return out
}
