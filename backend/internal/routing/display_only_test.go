package routing

import (
	"context"
	"encoding/json"
	"testing"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/model"
)

// displayOnlyFeatures：L1（仅展示）A→B 直达 10；L2 绕远 A→M→B 共 40。
func displayOnlyFeatures() []model.Feature {
	return []model.Feature{
		point("nA", "station", "A", 0, 0),
		point("nB", "station", "B", 10, 0),
		point("nM", "station", "M", 5, 10),
		line("e.L1.nA__nB", "nA", "nB", "L1", 10, map[string]any{"departDir": "e"}),
		line("e.L2.nA__nM", "nA", "nM", "L2", 20, map[string]any{"departDir": "s"}),
		line("e.L2.nM__nB", "nM", "nB", "L2", 20, map[string]any{"departDir": "n"}),
	}
}

func TestComputeCandidates_SkipsDisplayOnlyLines(t *testing.T) {
	g := buildGraph(t, displayOnlyFeatures())
	cfg := config.RouteConfig{MaxDistanceResults: 5, MaxPriceResults: 5, SearchWeightDistance: 0.5, SearchWeightPrice: 0.5, MinDirectResults: 1, MaxTransferResults: 3}
	input := ComputeInput{StartStation: "A", EndStation: "B", IsReverse: noReverse, Cfg: cfg, GlobalPricePerKm: 0.2}

	// 无仅展示线路：L1 直达最短
	all := ComputeCandidates(context.Background(), g, input)
	if len(all) == 0 || all[0].LineIDSequence[0] != "L1" {
		t.Fatalf("expected L1 direct first, got %+v", all)
	}

	input.IsDisplayOnly = func(id string) bool { return id == "L1" }
	got := ComputeCandidates(context.Background(), g, input)
	if len(got) == 0 {
		t.Fatal("expected L2 route when L1 is display-only")
	}
	for _, p := range got {
		legs := []model.RoutePath{p}
		if p.Journey != nil {
			legs = p.Journey.Legs
		}
		for _, leg := range legs {
			for _, id := range leg.LineIDSequence {
				if id == "L1" {
					t.Fatalf("route uses display-only line L1: %+v", p)
				}
			}
		}
	}

	// 只有仅展示线路经过的车站：无任何候选
	input.IsDisplayOnly = func(id string) bool { return id == "L1" || id == "L2" }
	if routes := ComputeCandidates(context.Background(), g, input); len(routes) != 0 {
		t.Fatalf("expected no routes when all lines are display-only, got %d", len(routes))
	}
}

func TestService_LinesChangeInvalidatesCache(t *testing.T) {
	b, err := json.Marshal(fc(displayOnlyFeatures()))
	if err != nil {
		t.Fatal(err)
	}
	source := &fakeGeoSource{geojson: b, version: "v1", lines: []byte(`[{"id":"L1"},{"id":"L2"}]`), linesVer: "l1"}
	cfg := config.RouteConfig{MaxDistanceResults: 5, MaxPriceResults: 5, SearchWeightDistance: 0.5, SearchWeightPrice: 0.5, MinDirectResults: 1}
	svc := NewService(source, func() config.RouteConfig { return cfg }, func() float64 { return 0.2 })

	first, err := svc.Query(context.Background(), "A", "B")
	if err != nil || len(first) == 0 || first[0].LineIDSequence[0] != "L1" {
		t.Fatalf("expected L1 first, got %+v err=%v", first, err)
	}

	source.lines = []byte(`[{"id":"L1","displayOnly":true},{"id":"L2"}]`)
	source.linesVer = "l2"
	second, err := svc.Query(context.Background(), "A", "B")
	if err != nil || len(second) == 0 {
		t.Fatalf("expected routes after marking L1 display-only, err=%v", err)
	}
	for _, p := range second {
		if p.LineIDSequence[0] == "L1" {
			t.Fatalf("cached result still uses display-only L1: %+v", p)
		}
	}
}
