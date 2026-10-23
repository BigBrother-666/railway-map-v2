package routing

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/model"
)

// TestRealGraphTiming 用真实地图数据测寻路耗时，仅在设置 RBENCH_DIR 时运行。RBENCH_DIR 下放
// kv_snapshot 导出的 geo.json（geojson FeatureCollection）与 lines.json（线路数组，取折返站）。
//
//	RBENCH_DIR=/path/to/dump go test ./internal/routing/ -run TestRealGraphTiming -v
func TestRealGraphTiming(t *testing.T) {
	dir := os.Getenv("RBENCH_DIR")
	if dir == "" {
		t.Skip("RBENCH_DIR not set")
	}
	b, err := os.ReadFile(filepath.Join(dir, "geo.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fc model.FeatureCollection
	if err := json.Unmarshal(b, &fc); err != nil {
		t.Fatal(err)
	}
	g, _ := BuildFromGeoJSON(&fc)
	reverse := map[string]struct{}{}
	if lb, err := os.ReadFile(filepath.Join(dir, "lines.json")); err == nil {
		var lines []lineProps
		_ = json.Unmarshal(lb, &lines)
		for _, l := range lines {
			for _, s := range l.ReverseStations {
				reverse[l.ID+"|"+s] = struct{}{}
			}
		}
	}
	isReverse := func(lineID, st string) bool { _, ok := reverse[lineID+"|"+st]; return ok }
	cfg := config.RouteConfig{MaxDistanceResults: 4, MaxPriceResults: 4, SearchWeightDistance: 0.5, SearchWeightPrice: 0.5,
		MinDirectResults: 1, MaxTransferResults: 1, TransferMinImprovement: 0.1}

	names := g.AllStationNames()
	sort.Strings(names)
	r := rand.New(rand.NewSource(1))
	type timing struct {
		a, b string
		d    time.Duration
	}
	var all []timing
	var total time.Duration
	for i := 0; i < 200; i++ {
		a, b := names[r.Intn(len(names))], names[r.Intn(len(names))]
		if a == b {
			continue
		}
		s := time.Now()
		ComputeCandidates(context.Background(), g, ComputeInput{StartStation: a, EndStation: b, IsReverse: isReverse, Cfg: cfg, GlobalPricePerKm: 1})
		d := time.Since(s)
		total += d
		all = append(all, timing{a, b, d})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].d > all[j].d })
	t.Logf("nodes=%d stations=%d pairs=%d total=%v", len(g.Nodes), len(names), len(all), total)
	for _, x := range all[:min(5, len(all))] {
		t.Logf("%-12v %s -> %s", x.d, x.a, x.b)
	}
}
