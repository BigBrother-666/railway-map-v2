package tilesource

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/geo"
)

func solidJPEG(t *testing.T, c color.Color, size int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func near(a, b uint8) bool { return a-b < 8 || b-a < 8 }

func TestComposeTiles(t *testing.T) {
	red := Tile{Data: solidJPEG(t, color.RGBA{255, 0, 0, 255}, 128), ContentType: "image/jpeg"}
	blue := Tile{Data: solidJPEG(t, color.RGBA{0, 0, 255, 255}, 128), ContentType: "image/jpeg"}

	got, err := composeTiles([4]Tile{red, blue, blue, red}, 128, 90)
	if err != nil || got.ContentType != "image/jpeg" {
		t.Fatalf("compose = %q %v", got.ContentType, err)
	}
	img, _, err := image.Decode(bytes.NewReader(got.Data))
	if err != nil || img.Bounds().Dx() != 128 {
		t.Fatalf("decode: %v %v", img.Bounds(), err)
	}
	for _, c := range []struct {
		x, y    int
		r, g, b uint8
	}{{32, 32, 255, 0, 0}, {96, 32, 0, 0, 255}, {32, 96, 0, 0, 255}, {96, 96, 255, 0, 0}} {
		r, g, b, _ := img.At(c.x, c.y).RGBA()
		if !near(uint8(r>>8), c.r) || !near(uint8(g>>8), c.g) || !near(uint8(b>>8), c.b) {
			t.Errorf("(%d,%d) = %d,%d,%d", c.x, c.y, r>>8, g>>8, b>>8)
		}
	}

	// 含空瓦片：PNG 且空白处透明
	got, err = composeTiles([4]Tile{red, blankTile(), blankTile(), blankTile()}, 128, 90)
	if err != nil || got.ContentType != "image/png" {
		t.Fatalf("partial = %q %v", got.ContentType, err)
	}
	pimg, _ := png.Decode(bytes.NewReader(got.Data))
	if _, _, _, a := pimg.At(100, 100).RGBA(); a != 0 {
		t.Errorf("blank quadrant alpha = %d", a)
	}
	if _, _, _, a := pimg.At(10, 10).RGBA(); a == 0 {
		t.Error("filled quadrant transparent")
	}

	// 全空 → 空瓦片
	if got, _ := composeTiles([4]Tile{blankTile(), blankTile(), blankTile(), blankTile()}, 128, 90); !isBlank(got) {
		t.Error("all-blank compose should be blank")
	}
}

// 上游最粗一级（z9）之上的层级由后端合成：z8 一张 = 上游 z9 的 4 张，再次请求命中缓存。
func newSynthUpstream(t *testing.T, hits *atomic.Int32, fail *atomic.Bool) *httptest.Server {
	t.Helper()
	tile := solidJPEG(t, color.RGBA{0, 200, 0, 255}, 128)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "dynmap_config.json"):
			_, _ = io.WriteString(w, testDynmapConfig)
		case strings.Contains(r.URL.Path, "/tiles/Paralon/flat/"):
			hits.Add(1)
			if fail != nil && fail.Load() {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(tile)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(up.Close)
	return up
}

func TestSynthesizedLevels(t *testing.T) {
	tile := solidJPEG(t, color.RGBA{0, 200, 0, 255}, 128)
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "dynmap_config.json"):
			_, _ = io.WriteString(w, testDynmapConfig)
		case strings.Contains(r.URL.Path, "/tiles/Paralon/flat/") && strings.Contains(r.URL.Path, "/zzzzz_"):
			hits.Add(1)
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(tile)
		default:
			http.NotFound(w, r)
		}
	}))
	defer up.Close()

	levels := 2
	cfg := config.TilesConfig{
		Sources: []config.TileSourceConfig{{ID: "a", Type: "dynmap", URL: up.URL + "/map"}},
		Worlds:  map[string][]config.WorldLayerConfig{"Paralon": {{External: &config.ExternalLayerConfig{Source: "a", SynthZoomLevels: &levels}}}},
	}
	config.ApplyTilesDefaults(&cfg)
	s := New(func() config.TilesConfig { return cfg }, nil, quiet)
	s.refresh(context.Background())

	if g := s.WorldTiles()["Paralon"]; g.MinNativeZoom != 7 || g.MinZoom != 5 || g.MaxNativeZoom != 14 {
		t.Errorf("worldTiles = %+v", g)
	}
	coarse := dynmapNativeZoom - 5 // 9
	for i := 0; i < 2; i++ {
		got, err := s.Fetch(context.Background(), "a", "Paralon", coarse-1, 1<<(coarse-2), 1<<(coarse-2))
		if err != nil || got.ContentType != "image/jpeg" {
			t.Fatalf("synth fetch = %q %v", got.ContentType, err)
		}
	}
	if n := hits.Load(); n != 4 {
		t.Errorf("upstream hits = %d, want 4", n)
	}
	// z7 = 4 张 z8；其中左上那张已缓存，只需 12 张新的上游瓦片
	if _, err := s.Fetch(context.Background(), "a", "Paralon", coarse-2, 1<<(coarse-3), 1<<(coarse-3)); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 16 {
		t.Errorf("upstream hits = %d, want 16", n)
	}
	// 超出合成层级 → 空瓦片，不访问上游
	got, err := s.Fetch(context.Background(), "a", "Paralon", coarse-3, 0, 0)
	if err != nil || !isBlank(got) || hits.Load() != 16 {
		t.Errorf("beyond synth = %v %v hits=%d", isBlank(got), err, hits.Load())
	}
}

// 合成层级只覆盖线路 / 车站范围加边距：范围外直接空白、不访问上游；原生层级不受限制。
func TestSynthesisExtent(t *testing.T) {
	var hits atomic.Int32
	up := newSynthUpstream(t, &hits, nil)
	levels := 2
	cfg := config.TilesConfig{
		Sources: []config.TileSourceConfig{{ID: "a", Type: "dynmap", URL: up.URL + "/map"}},
		Worlds:  map[string][]config.WorldLayerConfig{"Paralon": {{External: &config.ExternalLayerConfig{Source: "a", SynthZoomLevels: &levels, SynthMarginBlocks: 100}}}},
	}
	config.ApplyTilesDefaults(&cfg)
	bounds := map[string]geo.Bounds{"Paralon": {MinX: 0, MinZ: 0, MaxX: 10, MaxZ: 10}}
	s := New(func() config.TilesConfig { return cfg }, func() map[string]geo.Bounds { return bounds }, quiet)
	s.refresh(context.Background())

	// z8（合成，比上游最粗 z9 粗一级）：一张瓦片 = 4 张 z9；范围 [-100, 110] 只落在其中少数几张里。
	z := dynmapNativeZoom - 6
	half := 1 << (z - 1)
	got, err := s.Fetch(context.Background(), "a", "Paralon", z, half, half-1)
	if err != nil || isBlank(got) {
		t.Fatalf("in-extent synth = blank:%v %v", isBlank(got), err)
	}
	if n := hits.Load(); n == 0 || n >= 4 {
		t.Errorf("upstream hits = %d, want 1..3", n)
	}
	before := hits.Load()
	got, err = s.Fetch(context.Background(), "a", "Paralon", z, half+20, half+20)
	if err != nil || !isBlank(got) || hits.Load() != before {
		t.Errorf("out-of-extent synth = blank:%v %v hits %d→%d", isBlank(got), err, before, hits.Load())
	}
	if _, err := s.Fetch(context.Background(), "a", "Paralon", dynmapNativeZoom, 1<<13+500, 1<<13+500); err != nil || hits.Load() != before+1 {
		t.Errorf("native tile outside extent should still be fetched: %v hits=%d", err, hits.Load())
	}
}

// 过期瓦片先返回旧数据，后台刷新；上游失败时保留旧瓦片。
func TestStaleWhileRevalidate(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	up := newSynthUpstream(t, &hits, &fail)
	cfg := config.TilesConfig{Sources: []config.TileSourceConfig{{ID: "a", Type: "dynmap", URL: up.URL + "/map"}}}
	config.ApplyTilesDefaults(&cfg)
	s := New(func() config.TilesConfig { return cfg }, nil, quiet)
	s.refresh(context.Background())

	half := 1 << (dynmapNativeZoom - 1)
	fetch := func() (Tile, error) {
		return s.Fetch(context.Background(), "a", "Paralon", dynmapNativeZoom, half+3, half+3)
	}
	expire := func() {
		s.cache.mu.Lock()
		for _, el := range s.cache.items {
			el.Value.(*cacheEntry).expires = time.Now().Add(-time.Second)
		}
		s.cache.mu.Unlock()
	}
	first, err := fetch()
	if err != nil || hits.Load() != 1 {
		t.Fatalf("first fetch: %v hits=%d", err, hits.Load())
	}
	expire()
	fail.Store(true)
	if got, err := fetch(); err != nil || !bytes.Equal(got.Data, first.Data) {
		t.Fatalf("stale fetch should return old tile: %v", err)
	}
	waitFor(t, func() bool { return hits.Load() == 2 })
	// 刷新失败：旧瓦片仍在
	if got, err := fetch(); err != nil || !bytes.Equal(got.Data, first.Data) {
		t.Errorf("old tile lost after failed refresh: %v", err)
	}
	waitFor(t, func() bool { return hits.Load() == 3 })
	fail.Store(false)
	_, _ = fetch()
	waitFor(t, func() bool {
		_, stale, ok := s.cache.get(firstKey(s), time.Now())
		return ok && !stale
	})
}

func firstKey(s *Service) string {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	for k := range s.cache.items {
		return k
	}
	return ""
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
