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

	geom, meters, complete := c.RouteGeometry([]string{"A", "S", "B"})
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

	geom, _, complete := c.RouteGeometry([]string{"A", "S", "X"})
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
	if _, _, ok := c.RouteGeometry([]string{"A", "S"}); !ok {
		t.Fatalf("expected edge A→S before update")
	}
	c.SetGeojson([]byte(`{"type":"FeatureCollection","features":[]}`))
	if geom, _, ok := c.RouteGeometry([]string{"A", "S"}); ok || geom != nil {
		t.Fatalf("expected no geometry after update, got %+v", geom)
	}
}
