package routing

import (
	"context"
	"encoding/json"
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

	plans := FindTransferJourneys(ctx, g, "A", "E", 0, 0, noReverse)
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
	if plans := FindTransferJourneys(context.Background(), g, "A", "A", 0, 0, noReverse); len(plans) != 0 {
		t.Errorf("expected empty, got %d", len(plans))
	}
}

func TestFindTransferJourneys_MinImprovementFilters(t *testing.T) {
	g := transferScenario(t)
	ctx := context.Background()
	// 阈值 = 100 * (1-0.9) = 10km；换乘总距 20km 不满足，被过滤
	if plans := FindTransferJourneys(ctx, g, "A", "E", 3, 0.9, noReverse); len(plans) != 0 {
		t.Errorf("expected filtered out, got %d", len(plans))
	}
	// 0.2 时阈值 80km，20km 满足
	if plans := FindTransferJourneys(ctx, g, "A", "E", 3, 0.2, noReverse); len(plans) == 0 {
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
		line("e.L.bIn1__nB1", "bIn1", "nB1", "L", 1, map[string]any{"departDir": "s"}),
		line("e.L.bIn1__bOut1", "bIn1", "bOut1", "L", 1, map[string]any{"departDir": "e"}),
		line("e.L.bOut1__mid", "bOut1", "mid", "L", 10, map[string]any{"departDir": "e"}),
		line("e.L.mid__bIn2", "mid", "bIn2", "L", 10, map[string]any{"departDir": "e"}),
		line("e.L.bIn2__nB2", "bIn2", "nB2", "L", 1, map[string]any{"departDir": "s"}),
		line("e.L.bIn2__bOut2", "bIn2", "bOut2", "L", 1, map[string]any{"departDir": "e"}),
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
		line("e.L.rIn__nR2", "rIn", "nR2", "L", 10, map[string]any{"departDir": "s"}),
		line("e.L.rIn__dead", "rIn", "dead", "L", 1, map[string]any{"departDir": "e"}),
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

// platformNameOfMainlineSwitch 是寻路内部用来识别「正线绕行」进站道岔的辅助方法
// （复刻插件 GeoRouteGraph 同名方法），只在 hasMainlineBypass / getNodeStationName 内部使用。
func TestGraph_PlatformNameOfMainlineSwitch(t *testing.T) {
	g := buildGraph(t, []model.Feature{
		point("in", "switch", "", 0, 0),
		point("platform", "station", "P", 10, 10),
		point("out", "switch", "", 20, 0),
		line("e.L.in__platform", "in", "platform", "L", 5, map[string]any{"departDir": "s"}),
		line("e.L.in__out", "in", "out", "L", 5, map[string]any{"departDir": "e"}),
	})
	if name := g.platformNameOfMainlineSwitch("in", "L"); name != "P" {
		t.Errorf("platformNameOfMainlineSwitch = %q, want P", name)
	}
}
