package geo

import "testing"

func TestBuildWorldBounds(t *testing.T) {
	payload := []byte(`{"type":"FeatureCollection","features":[
	 {"properties":{"world":"a","type":"station"},"geometry":{"type":"Point","coordinates":[10,-5,64]}},
	 {"properties":{"world":"a"},"geometry":{"type":"LineString","coordinates":[[-20,3,64],[40,7,64]]}},
	 {"properties":{"world":"b"},"geometry":{"type":"Point","coordinates":[1,2]}},
	 {"properties":{},"geometry":{"type":"Point","coordinates":[999,999]}}
	]}`)
	got := buildWorldBounds(payload)
	if a := got["a"]; a != (Bounds{MinX: -20, MinZ: -5, MaxX: 40, MaxZ: 7}) {
		t.Errorf("a = %+v", a)
	}
	if b := got["b"]; b != (Bounds{MinX: 1, MinZ: 2, MaxX: 1, MaxZ: 2}) || len(got) != 2 {
		t.Errorf("got = %+v", got)
	}
	if len(buildWorldBounds(nil)) != 0 {
		t.Error("empty payload")
	}
}
