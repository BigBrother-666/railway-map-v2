package geo

import (
	"reflect"
	"testing"
)

// testGeojson 构造 A→S→B 三节点、两段同线路轨道的 geojson（A→S 含共线中间点）。
const testGeojson = `{"type":"FeatureCollection","features":[
 {"type":"Feature","properties":{"id":"A","type":"station","world":"w"},"geometry":{"type":"Point","coordinates":[0,0,64]}},
 {"type":"Feature","properties":{"id":"S","type":"switch","world":"w"},"geometry":{"type":"Point","coordinates":[10,0,64]}},
 {"type":"Feature","properties":{"id":"B","type":"station","world":"w"},"geometry":{"type":"Point","coordinates":[10,10,64]}},
 {"type":"Feature","properties":{"id":"e1","from":"A","to":"S","lineId":"L1","world":"w","color":"#f00","length":10},
  "geometry":{"type":"LineString","coordinates":[[0,0,64],[5,0,64],[10,0,64]]}},
 {"type":"Feature","properties":{"id":"e2","from":"S","to":"B","lineId":"L1","world":"w","color":"#f00","length":10},
  "geometry":{"type":"LineString","coordinates":[[10,0,64],[10,10,64]]}}
]}`

// TestRouteGeometryMergesSameLineSegments 校验同线路相邻段合并为一条折线、共线点被简化、起终点坐标正确。
func TestRouteGeometryMergesSameLineSegments(t *testing.T) {
	c := &Cache{}
	c.SetGeojson([]byte(testGeojson))

	geom, meters, complete := c.RouteGeometry([]string{"A", "S", "B"}, nil, nil)
	if geom == nil || !complete {
		t.Fatalf("expected complete geometry, got geom=%v complete=%v", geom, complete)
	}
	if meters != 20 {
		t.Fatalf("expected 20m, got %v", meters)
	}
	if geom.World != "w" || len(geom.Segments) != 1 {
		t.Fatalf("expected one merged segment in world w, got %+v", geom)
	}
	want := [][]float64{{0, 0}, {10, 0}, {10, 10}}
	if !reflect.DeepEqual(geom.Segments[0].Coords, want) {
		t.Fatalf("unexpected coords: got %v want %v", geom.Segments[0].Coords, want)
	}
	if !reflect.DeepEqual(geom.Start, []float64{0, 0}) || !reflect.DeepEqual(geom.End, []float64{10, 10}) {
		t.Fatalf("unexpected endpoints: %v %v", geom.Start, geom.End)
	}
}

// TestRouteGeometryReportsMissingEdge 校验缺段时返回 complete=false，但已找到的段仍保留。
func TestRouteGeometryReportsMissingEdge(t *testing.T) {
	c := &Cache{}
	c.SetGeojson([]byte(testGeojson))

	geom, _, complete := c.RouteGeometry([]string{"A", "S", "X"}, nil, nil)
	if complete {
		t.Fatalf("expected incomplete geometry")
	}
	if geom == nil || len(geom.Segments) != 1 {
		t.Fatalf("expected partial geometry, got %+v", geom)
	}
}

// TestRouteGeometryRebuildsAfterGeojsonUpdate 校验 geojson 更新后索引按新版本重建。
func TestRouteGeometryRebuildsAfterGeojsonUpdate(t *testing.T) {
	c := &Cache{}
	c.SetGeojson([]byte(testGeojson))
	if _, _, ok := c.RouteGeometry([]string{"A", "S"}, nil, nil); !ok {
		t.Fatalf("expected edge A→S before update")
	}
	c.SetGeojson([]byte(`{"type":"FeatureCollection","features":[]}`))
	if geom, _, ok := c.RouteGeometry([]string{"A", "S"}, nil, nil); ok || geom != nil {
		t.Fatalf("expected no geometry after update, got %+v", geom)
	}
}

// TestRouteGeometryUsesLineConfigColor 校验快照颜色取线路配置而非 geojson color，缺配置时用兜底色。
func TestRouteGeometryUsesLineConfigColor(t *testing.T) {
	c := &Cache{}
	c.SetGeojson([]byte(testGeojson))
	geom, _, _ := c.RouteGeometry([]string{"A", "S", "B"}, nil, nil)
	if geom.Segments[0].Color != DefaultLineColor {
		t.Fatalf("expected default color without line config, got %s", geom.Segments[0].Color)
	}
	if err := c.SetPrivateLines([]byte(`[{"id":"L1","color":"#00AA00"}]`)); err != nil {
		t.Fatal(err)
	}
	geom, _, _ = c.RouteGeometry([]string{"A", "S", "B"}, nil, nil)
	if geom.Segments[0].Color != "#00AA00" {
		t.Fatalf("expected line config color, got %s", geom.Segments[0].Color)
	}
}

// sharedGeojson：A→S→B 为 L1 / L2 共线（L2 要素在后，旧索引会被它覆盖），B→C 仅 L1。
const sharedGeojson = `{"type":"FeatureCollection","features":[
 {"type":"Feature","properties":{"id":"A","type":"station","world":"w"},"geometry":{"type":"Point","coordinates":[0,0,64]}},
 {"type":"Feature","properties":{"id":"S","type":"switch","world":"w"},"geometry":{"type":"Point","coordinates":[10,0,64]}},
 {"type":"Feature","properties":{"id":"B","type":"station","world":"w"},"geometry":{"type":"Point","coordinates":[20,0,64]}},
 {"type":"Feature","properties":{"id":"C","type":"station","world":"w"},"geometry":{"type":"Point","coordinates":[30,0,64]}},
 {"type":"Feature","properties":{"id":"AS1","from":"A","to":"S","lineId":"L1","world":"w","length":10},"geometry":{"type":"LineString","coordinates":[[0,0],[10,0]]}},
 {"type":"Feature","properties":{"id":"AS2","from":"A","to":"S","lineId":"L2","world":"w","length":10},"geometry":{"type":"LineString","coordinates":[[0,0],[10,0]]}},
 {"type":"Feature","properties":{"id":"SB1","from":"S","to":"B","lineId":"L1","world":"w","length":10},"geometry":{"type":"LineString","coordinates":[[10,0],[20,0]]}},
 {"type":"Feature","properties":{"id":"SB2","from":"S","to":"B","lineId":"L2","world":"w","length":10},"geometry":{"type":"LineString","coordinates":[[10,0],[20,0]]}},
 {"type":"Feature","properties":{"id":"BC1","from":"B","to":"C","lineId":"L1","world":"w","length":10},"geometry":{"type":"LineString","coordinates":[[20,0],[30,0]]}}
]}`

// segmentLines 返回快照各折线的线路 id。
func segmentLines(t *testing.T, c *Cache, nodeIDs, hints, lineIDs []string) []string {
	t.Helper()
	geom, _, complete := c.RouteGeometry(nodeIDs, hints, lineIDs)
	if geom == nil || !complete {
		t.Fatalf("expected complete geometry, got %+v", geom)
	}
	out := make([]string, len(geom.Segments))
	for i, s := range geom.Segments {
		out[i] = s.LineID
	}
	return out
}

// TestRouteGeometrySharedTrackFollowsContinuingLine 校验共线段归到沿路线延续的线路，而非 geojson 中最后出现的线路。
func TestRouteGeometrySharedTrackFollowsContinuingLine(t *testing.T) {
	c := &Cache{}
	c.SetGeojson([]byte(sharedGeojson))
	if got := segmentLines(t, c, []string{"A", "S", "B", "C"}, nil, nil); !reflect.DeepEqual(got, []string{"L1"}) {
		t.Fatalf("expected single L1 segment, got %v", got)
	}
}

// TestRouteGeometrySharedTrackUsesRideLines 校验共线段只在乘车经过的线路里选；无命中时回退推断。
func TestRouteGeometrySharedTrackUsesRideLines(t *testing.T) {
	c := &Cache{}
	c.SetGeojson([]byte(sharedGeojson))
	if got := segmentLines(t, c, []string{"A", "S", "B"}, nil, []string{"L2"}); !reflect.DeepEqual(got, []string{"L2"}) {
		t.Fatalf("expected L2 with ride lines, got %v", got)
	}
	if got := segmentLines(t, c, []string{"A", "S", "B"}, nil, []string{"X"}); len(got) != 1 {
		t.Fatalf("expected one merged segment on fallback, got %v", got)
	}
}

// TestRouteGeometrySharedTrackUsesSegmentHints 校验逐段 lineId 优先于连续性推断（快速车购票路线）。
func TestRouteGeometrySharedTrackUsesSegmentHints(t *testing.T) {
	c := &Cache{}
	c.SetGeojson([]byte(sharedGeojson))
	got := segmentLines(t, c, []string{"A", "S", "B", "C"}, []string{"L2", "L2", "L1"}, nil)
	if !reflect.DeepEqual(got, []string{"L2", "L1"}) {
		t.Fatalf("expected L2 then L1 from hints, got %v", got)
	}
}
