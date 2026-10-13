package routing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"railway-map-backend/internal/model"
)

// point 构造一个 Point feature（type=station/switch）。坐标为 [x, z, y]（与 geojson 一致）。
func point(id, typ, name string, x, z float64) model.Feature {
	props := map[string]any{"id": id, "type": typ, "world": "w"}
	if name != "" {
		props["name"] = name
	}
	return feature("Point", []float64{x, z, 64}, props)
}

// line 构造一个 LineString feature（一段有向边）。
func line(id, from, to, lineID string, length float64, extra map[string]any) model.Feature {
	props := map[string]any{
		"id": id, "from": from, "to": to, "lineId": lineID,
		"world": "w", "color": "#fff", "length": length,
	}
	for k, v := range extra {
		props[k] = v
	}
	return featureLine(props)
}

func feature(geomType string, coords []float64, props map[string]any) model.Feature {
	coordsJSON, _ := json.Marshal(coords)
	propsJSON, _ := json.Marshal(props)
	geomJSON, _ := json.Marshal(map[string]json.RawMessage{
		"type":        mustJSON(geomType),
		"coordinates": coordsJSON,
	})
	return model.Feature{Type: "Feature", Geometry: geomJSON, Properties: propsJSON}
}

func featureLine(props map[string]any) model.Feature {
	coordsJSON, _ := json.Marshal([][]float64{{0, 0, 64}, {1, 0, 64}})
	propsJSON, _ := json.Marshal(props)
	geomJSON, _ := json.Marshal(map[string]json.RawMessage{
		"type":        mustJSON("LineString"),
		"coordinates": coordsJSON,
	})
	return model.Feature{Type: "Feature", Geometry: geomJSON, Properties: propsJSON}
}

func mustJSON(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

func fc(features []model.Feature) *model.FeatureCollection {
	return &model.FeatureCollection{Type: "FeatureCollection", Features: features}
}

func buildGraph(t *testing.T, features []model.Feature) *Graph {
	t.Helper()
	g, err := BuildFromGeoJSON(fc(features))
	if err != nil {
		t.Fatalf("BuildFromGeoJSON: %v", err)
	}
	return g
}

func strPtr(s string) *string { return &s }

func noReverse(string, string) bool { return false }

func closeTo(t *testing.T, got, want, eps float64) {
	t.Helper()
	if got < want-eps || got > want+eps {
		t.Errorf("got %v, want %v (±%v)", got, want, eps)
	}
}

// 复刻插件 TransferJourneyTest：L1 从 A 绕远到 E，L2 从 B 短直达 E。B 的 L1/L2 站台是独立节点无连边。
func transferScenario(t *testing.T) *Graph {
	return buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nB1", "station", "B", 10, 0),
		point("nE1", "station", "E", 100, 0),
		point("nB2", "station", "B", 10, 20),
		point("nE2", "station", "E", 100, 20),
		line("e.L1.nA__nB1", "nA", "nB1", "L1", 10, map[string]any{"departDir": "e"}),
		line("e.L1.nB1__nE1", "nB1", "nE1", "L1", 90, map[string]any{"departDir": "e"}),
		line("e.L2.nB2__nE2", "nB2", "nE2", "L2", 10, map[string]any{"departDir": "s"}),
	})
}

func TestFindTransferJourneys_FindsCheaperTransfer(t *testing.T) {
	g := transferScenario(t)
	ctx := context.Background()

	direct := FindByStation(ctx, g, "A", "E", 0, noReverse)
	if len(direct) == 0 {
		t.Fatal("expected direct route")
	}
	closeTo(t, direct[0].Distance, 100.0/1000, 1e-9)

	plans := FindTransferJourneys(ctx, g, "A", "E", 0, 0, noReverse, nil)
	if len(plans) == 0 {
		t.Fatal("expected transfer plan")
	}
	best := plans[0]
	if best.TransferStations[0] != "B" {
		t.Errorf("transfer station = %q, want B", best.TransferStations[0])
	}
	if len(best.Legs) != 2 {
		t.Errorf("legs = %d, want 2", len(best.Legs))
	}
	closeTo(t, best.TotalDistance, 20.0/1000, 1e-9)
	if !(best.TotalDistance < direct[0].Distance) {
		t.Errorf("transfer distance %v should be less than direct %v", best.TotalDistance, direct[0].Distance)
	}
}

func TestFindTransferJourneys_SameStationReturnsEmpty(t *testing.T) {
	g := transferScenario(t)
	if plans := FindTransferJourneys(context.Background(), g, "A", "A", 0, 0, noReverse, nil); len(plans) != 0 {
		t.Errorf("expected empty, got %d", len(plans))
	}
}

func TestFindTransferJourneys_MinImprovementFilters(t *testing.T) {
	g := transferScenario(t)
	ctx := context.Background()
	// 阈值 = 100 * (1-0.9) = 10km；换乘总距 20km 不满足，被过滤
	if plans := FindTransferJourneys(ctx, g, "A", "E", 3, 0.9, noReverse, nil); len(plans) != 0 {
		t.Errorf("expected filtered out, got %d", len(plans))
	}
	// 0.2 时阈值 80km，20km 满足
	if plans := FindTransferJourneys(ctx, g, "A", "E", 3, 0.2, noReverse, nil); len(plans) == 0 {
		t.Error("expected plan to pass threshold 0.2")
	}
}

// 直行道岔 S：从 nA 到达面 "1_0"，只有到达面属于 {1_0} 的出边才能续接直行段到 D。
// 反向牌出边 enterFrom={9_9} 不含 1_0，应被门控拒绝，防止走出物理非法路线。
func TestFindByStation_EnterFaceGating_RejectsIllegalContinuation(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nS", "switch", "", 10, 0),
		point("nD", "station", "D", 20, 0),
		point("nX", "station", "X", 20, 10),
		// A->S 到达 S 的到达面为 1_0
		line("e.L1.nA__nS", "nA", "nS", "L1", 10, map[string]any{"departDir": "e", "enterTo": "1_0"}),
		// S->D 合法：允许到达面含 1_0
		line("e.L1.nS__nD", "nS", "nD", "L1", 10, map[string]any{"departDir": "e", "enterFrom": []string{"1_0"}}),
		// S->X 非法：只给到达面 9_9 的车（反向），从 1_0 来的车不能走
		line("e.L1.nS__nX", "nS", "nX", "L1", 5, map[string]any{"departDir": "s", "enterFrom": []string{"9_9"}}),
	})
	ctx := context.Background()
	toD := FindByStation(ctx, g, "A", "D", 0, noReverse)
	if len(toD) == 0 {
		t.Error("expected route to D")
	}
	toX := FindByStation(ctx, g, "A", "X", 0, noReverse)
	if len(toX) != 0 {
		t.Errorf("expected no route to X (gated), got %d", len(toX))
	}
}

func TestFindByStation_EnterFaceGating_AllowsWhenFieldsMissing(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nS", "switch", "", 10, 0),
		point("nX", "station", "X", 20, 10),
		line("e.L1.nA__nS", "nA", "nS", "L1", 10, map[string]any{"departDir": "e"}),
		line("e.L1.nS__nX", "nS", "nX", "L1", 5, map[string]any{"departDir": "s", "enterFrom": []string{"9_9"}}),
	})
	// inLink.enterTo 缺失 → 放行
	toX := FindByStation(context.Background(), g, "A", "X", 0, noReverse)
	if len(toX) == 0 {
		t.Error("expected route to X when enter-face fields missing (backward compat)")
	}
}

func TestFindByStation_RejectsRepeatingSameStationOnMainline(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("bIn1", "switch", "", 10, 0),
		point("bOut1", "switch", "", 20, 0),
		point("mid", "switch", "", 30, 0),
		point("bIn2", "switch", "", 40, 0),
		point("bOut2", "switch", "", 50, 0),
		point("nB1", "station", "B", 10, 10),
		point("nB2", "station", "B", 40, 10),
		point("nD", "station", "D", 60, 0),
		line("e.L.nA__bIn1", "nA", "bIn1", "L", 10, map[string]any{"departDir": "e"}),
		line("e.L.bIn1__nB1", "bIn1", "nB1", "L", 1, map[string]any{"departDir": "s", "track": "siding"}),
		line("e.L.bIn1__bOut1", "bIn1", "bOut1", "L", 1, map[string]any{"departDir": "e", "track": "main"}),
		line("e.L.bOut1__mid", "bOut1", "mid", "L", 10, map[string]any{"departDir": "e"}),
		line("e.L.mid__bIn2", "mid", "bIn2", "L", 10, map[string]any{"departDir": "e"}),
		line("e.L.bIn2__nB2", "bIn2", "nB2", "L", 1, map[string]any{"departDir": "s", "track": "siding"}),
		line("e.L.bIn2__bOut2", "bIn2", "bOut2", "L", 1, map[string]any{"departDir": "e", "track": "main"}),
		line("e.L.bOut2__nD", "bOut2", "nD", "L", 10, map[string]any{"departDir": "e"}),
	})
	if routes := FindByStation(context.Background(), g, "A", "D", 0, noReverse); len(routes) != 0 {
		t.Errorf("expected 0 routes (station B repeats), got %d", len(routes))
	}
}

func TestFindByStation_AllowsFinalArrivalAtSameNamedStation(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("nR", "station", "R", 0, 0),
		point("c1", "switch", "", 10, 0),
		point("rIn", "switch", "", 20, 0),
		point("nR2", "station", "R", 20, 10),
		point("dead", "switch", "", 30, 0),
		line("e.L.nR__c1", "nR", "c1", "L", 10, map[string]any{"departDir": "e"}),
		line("e.L.c1__rIn", "c1", "rIn", "L", 10, map[string]any{"departDir": "e"}),
		line("e.L.rIn__nR2", "rIn", "nR2", "L", 10, map[string]any{"departDir": "s", "track": "siding"}),
		line("e.L.rIn__dead", "rIn", "dead", "L", 1, map[string]any{"departDir": "e", "track": "main"}),
	})
	paths := FindByStation(context.Background(), g, "R", "R", 1, noReverse)
	if len(paths) != 1 {
		t.Fatalf("expected 1 path, got %d", len(paths))
	}
	want := []string{"nR", "c1", "rIn", "nR2"}
	got := paths[0].NodeIDs
	if len(got) != len(want) {
		t.Fatalf("nodeIds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("nodeIds[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// platformNameOfMainlineSwitch 是寻路内部用来识别进站道岔的辅助方法（复刻插件 GeoRouteGraph 同名方法），
// 按 track 标记判定：停靠线中途夹着道岔也能找到车站。
func TestGraph_PlatformNameOfMainlineSwitch(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("in", "switch", "", 0, 0),
		point("x", "switch", "", 5, 5),
		point("platform", "station", "P", 10, 10),
		point("out", "switch", "", 20, 0),
		line("e.L.in__x", "in", "x", "L", 5, map[string]any{"departDir": "s", "track": "siding"}),
		line("e.L.x__platform", "x", "platform", "L", 5, map[string]any{"departDir": "e", "track": "siding"}),
		line("e.L.in__out", "in", "out", "L", 5, map[string]any{"departDir": "e", "track": "main"}),
	})
	if name := g.platformNameOfMainlineSwitch("in", "L"); name != "P" {
		t.Errorf("platformNameOfMainlineSwitch = %q, want P", name)
	}
	if name := g.platformNameOfMainlineSwitch("x", "L"); name != "" {
		t.Errorf("intermediate switch platformNameOfMainlineSwitch = %q, want empty", name)
	}
}

// 共线车站：进站道岔上同时挂着两条线各自的正线与停靠线出边，每条线都应各自识别为进站道岔。
func TestGraph_PlatformNameOfMainlineSwitch_SharedStation(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("in", "switch", "", 0, 0),
		point("platform", "station", "P", 10, 10),
		point("out", "switch", "", 20, 0),
		line("e.L1.in__platform", "in", "platform", "L1", 5, map[string]any{"departDir": "s", "track": "siding"}),
		line("e.L1.in__out", "in", "out", "L1", 5, map[string]any{"departDir": "e", "track": "main"}),
		line("e.L2.in__platform", "in", "platform", "L2", 5, map[string]any{"departDir": "s", "track": "siding"}),
		line("e.L2.in__out", "in", "out", "L2", 5, map[string]any{"departDir": "e", "track": "main"}),
	})
	for _, lineID := range []string{"L1", "L2"} {
		if name := g.platformNameOfMainlineSwitch("in", lineID); name != "P" {
			t.Errorf("[%s] platformNameOfMainlineSwitch = %q, want P", lineID, name)
		}
	}
	if name := g.platformNameOfMainlineSwitch("in", "L3"); name != "" {
		t.Errorf("[L3] platformNameOfMainlineSwitch = %q, want empty", name)
	}
}

// 停靠线 platform 前后都夹着道岔：快速车 A→C 必须走正线跨越 B，站序仍含 B；A→B 可经停靠线到达。
func TestFindByStation_SkipsStationReachedViaSiding(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("in", "switch", "", 10, 0),
		point("x", "switch", "", 15, 5),
		point("nB", "station", "B", 20, 5),
		point("y", "switch", "", 25, 5),
		point("out", "switch", "", 30, 0),
		point("nC", "station", "C", 40, 0),
		line("e.L.nA__in", "nA", "in", "L", 10, nil),
		line("e.L.in__x", "in", "x", "L", 1, map[string]any{"departDir": "s", "track": "siding"}),
		line("e.L.x__nB", "x", "nB", "L", 1, map[string]any{"departDir": "e", "track": "siding"}),
		line("e.L.nB__y", "nB", "y", "L", 1, nil),
		line("e.L.y__out", "y", "out", "L", 1, map[string]any{"departDir": "e"}),
		line("e.L.in__out", "in", "out", "L", 50, map[string]any{"departDir": "e", "track": "main"}),
		line("e.L.out__nC", "out", "nC", "L", 10, map[string]any{"departDir": "e"}),
	})
	ctx := context.Background()
	ac := FindByStation(ctx, g, "A", "C", 0, noReverse)
	if len(ac) != 1 {
		t.Fatalf("A->C expected 1 path, got %d", len(ac))
	}
	if got := strings.Join(ac[0].NodeIDs, ","); got != "nA,in,out,nC" {
		t.Errorf("A->C nodeIds = %s, want nA,in,out,nC", got)
	}
	if got := strings.Join(ac[0].Stations, ","); got != "A,B,C" {
		t.Errorf("A->C stations = %s, want A,B,C", got)
	}
	if ab := FindByStation(ctx, g, "A", "B", 0, noReverse); len(ab) == 0 {
		t.Error("A->B expected a path via siding")
	}
}

// groupScenario 构造两条互不相连的线路：line1 甲→乙，line2 丙→丁。乙、丙之间没有任何轨道。
func groupScenario(t *testing.T) *Graph {
	return buildGraph(t, []model.Feature{
		point("n甲", "station", "甲", 0, 0),
		point("n乙", "station", "乙", 10, 0),
		point("n丙", "station", "丙", 12, 0),
		point("n丁", "station", "丁", 30, 0),
		line("e.line1.n甲__n乙", "n甲", "n乙", "line1", 10, map[string]any{"departDir": "e"}),
		line("e.line2.n丙__n丁", "n丙", "n丁", "line2", 18, map[string]any{"departDir": "e"}),
	})
}

// TestFindTransferJourneys_StationGroup 验证站组换乘：乙、丙同组时可在乙下车、丙上车组成联程票；无站组时找不到方案。
func TestFindTransferJourneys_StationGroup(t *testing.T) {
	g := groupScenario(t)
	ctx := context.Background()

	if plans := FindTransferJourneys(ctx, g, "甲", "丁", 0, 0, noReverse, nil); len(plans) != 0 {
		t.Fatalf("without group expected no plan, got %d", len(plans))
	}

	group := []string{"乙", "丙"}
	members := func(name string) []string {
		if name == "乙" || name == "丙" {
			return group
		}
		return nil
	}
	plans := FindTransferJourneys(ctx, g, "甲", "丁", 0, 0, noReverse, members)
	if len(plans) != 1 {
		t.Fatalf("with group expected 1 plan, got %d", len(plans))
	}
	p := plans[0]
	if len(p.Legs) != 2 {
		t.Fatalf("legs = %d, want 2", len(p.Legs))
	}
	leg1, leg2 := p.Legs[0].Stations, p.Legs[1].Stations
	if leg1[0] != "甲" || leg1[len(leg1)-1] != "乙" {
		t.Errorf("leg1 stations = %v, want 甲…乙", leg1)
	}
	if leg2[0] != "丙" || leg2[len(leg2)-1] != "丁" {
		t.Errorf("leg2 stations = %v, want 丙…丁", leg2)
	}
	if len(p.TransferStations) != 1 || p.TransferStations[0] != "乙" {
		t.Errorf("transferStations = %v, want [乙]", p.TransferStations)
	}
	closeTo(t, p.TotalDistance, 28.0/1000, 1e-9)
}
