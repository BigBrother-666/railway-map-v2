package routing

import (
	"context"
	"testing"

	"railway-map-backend/internal/model"
)

func TestReachableIgnoringRevisit_RejectsWhenEnterFaceGatingBlocksAllPaths(t *testing.T) {
	// 复刻 pathfind_test.go 里 TestFindByStation_EnterFaceGating_RejectsIllegalContinuation 的图：
	// A -> S（到达面 1_0） -> D（允许 1_0，合法） / -> X（只允许 9_9，非法）。
	g := buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nS", "switch", "", 10, 0),
		point("nD", "station", "D", 20, 0),
		point("nX", "station", "X", 20, 10),
		line("e.L1.nA__nS", "nA", "nS", "L1", 10, map[string]any{"departDir": "e", "enterTo": "1_0"}),
		line("e.L1.nS__nD", "nS", "nD", "L1", 10, map[string]any{"departDir": "e", "enterFrom": []string{"1_0"}}),
		line("e.L1.nS__nX", "nS", "nX", "L1", 5, map[string]any{"departDir": "s", "enterFrom": []string{"9_9"}}),
	})
	ctx := context.Background()
	if !reachableIgnoringRevisit(ctx, g, "nA", "D", noReverse) {
		t.Error("expected A reachable to D (enter-face allows)")
	}
	if reachableIgnoringRevisit(ctx, g, "nA", "X", noReverse) {
		t.Error("expected A NOT reachable to X (enter-face gating blocks the only path)")
	}
}

func TestReachableIgnoringRevisit_AllowsWhenGatingFieldsMissing(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nS", "switch", "", 10, 0),
		point("nX", "station", "X", 20, 10),
		line("e.L1.nA__nS", "nA", "nS", "L1", 10, map[string]any{"departDir": "e"}),
		line("e.L1.nS__nX", "nS", "nX", "L1", 5, map[string]any{"departDir": "s", "enterFrom": []string{"9_9"}}),
	})
	if !reachableIgnoringRevisit(context.Background(), g, "nA", "X", noReverse) {
		t.Error("expected reachable when inLink.enterTo missing (backward compat, matches enterFaceAllows)")
	}
}

func TestReachableIgnoringRevisit_RequiresAtLeastOneHop(t *testing.T) {
	// 单节点图（起点本身就是终点站名，但没有任何出边）：不应认为「原地不动」就算命中。
	g := buildGraph(t, []model.Feature{
		point("nR", "station", "R", 0, 0),
	})
	if reachableIgnoringRevisit(context.Background(), g, "nR", "R", noReverse) {
		t.Error("expected NOT reachable: start node has no outgoing edges, standing still must not count as arrival")
	}
}

func TestReachableIgnoringRevisit_LoopBackToStartCountsAsReachable(t *testing.T) {
	// 复刻 pathfind_test.go 里 TestFindByStation_AllowsFinalArrivalAtSameNamedStation 的图：
	// R --c1--> rIn --> R2（同名 R）；一圈回到同名站，走过至少一条边，应判定可达。
	g := buildGraph(t, []model.Feature{
		point("nR", "station", "R", 0, 0),
		point("c1", "switch", "", 10, 0),
		point("rIn", "switch", "", 20, 0),
		point("nR2", "station", "R", 20, 10),
		point("dead", "switch", "", 30, 0),
		line("e.L.nR__c1", "nR", "c1", "L", 10, map[string]any{"departDir": "e"}),
		line("e.L.c1__rIn", "c1", "rIn", "L", 10, map[string]any{"departDir": "e"}),
		line("e.L.rIn__nR2", "rIn", "nR2", "L", 10, map[string]any{"departDir": "s"}),
		line("e.L.rIn__dead", "rIn", "dead", "L", 1, map[string]any{"departDir": "e"}),
	})
	if !reachableIgnoringRevisit(context.Background(), g, "nR", "R", noReverse) {
		t.Error("expected reachable: looping back to a same-named station after at least one hop counts as arrival")
	}
}

func TestReachableIgnoringRevisit_NoPathAtAllReturnsFalse(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nB", "station", "B", 100, 100), // 与 A 完全不连通
	})
	if reachableIgnoringRevisit(context.Background(), g, "nA", "B", noReverse) {
		t.Error("expected NOT reachable: no edges connect A's component to B")
	}
}

// TestKShortest_ShortCircuitsWithoutRunningFullEnumeration 验证：真正不可达的站点对（enter-face 门控
// 挡死唯一路径）触发短路时，kShortest 完全不会进入原来的完整枚举循环——证明优化确实生效，
// 不是「图太小所以碰巧一样快」。
func TestKShortest_ShortCircuitsWithoutRunningFullEnumeration(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nS", "switch", "", 10, 0),
		point("nX", "station", "X", 20, 10),
		line("e.L1.nA__nS", "nA", "nS", "L1", 10, map[string]any{"departDir": "e", "enterTo": "1_0"}),
		line("e.L1.nS__nX", "nS", "nX", "L1", 5, map[string]any{"departDir": "s", "enterFrom": []string{"9_9"}}),
	})

	fullSearchRan := false
	testKShortestFullSearchHook = func() { fullSearchRan = true }
	defer func() { testKShortestFullSearchHook = nil }()

	results := kShortest(context.Background(), g, "nA", "X", 16, noReverse)
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
	if fullSearchRan {
		t.Error("expected kShortest to short-circuit before entering the full enumeration loop")
	}
}

// TestKShortest_RunsFullEnumerationWhenReachable 对照组：可达时短路必须交给完整逻辑正常跑，
// 确保短路只拦截真正不可达的情况，不影响任何有解查询。
func TestKShortest_RunsFullEnumerationWhenReachable(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nD", "station", "D", 10, 0),
		line("e.L.nA__nD", "nA", "nD", "L", 10, map[string]any{"departDir": "e"}),
	})

	fullSearchRan := false
	testKShortestFullSearchHook = func() { fullSearchRan = true }
	defer func() { testKShortestFullSearchHook = nil }()

	results := kShortest(context.Background(), g, "nA", "D", 16, noReverse)
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if !fullSearchRan {
		t.Error("expected kShortest to run the full enumeration loop for a reachable pair")
	}
}
