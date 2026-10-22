package tilesource

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"railway-map-backend/internal/config"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// 取自真实 dynmap 3.7 的 dynmap_config.json（精简）：flat 为俯视，surface 为 30° 等距视角。
const testDynmapConfig = `{"worlds":[
 {"name":"Paralon","maps":[
  {"type":"HDMapType","name":"surface","prefix":"t","tilescale":0,"mapzoomout":5,"mapzoomin":4,"image-format":"jpg",
   "worldtomap":[2.82842712474619,0.0,-2.8284271247461903,-1.4142135623730954,3.4641016151377544,-1.4142135623730951,5.551115123125782e-17,0.9999999999999997,5.551115123125782e-17]},
  {"type":"HDMapType","name":"flat","prefix":"flat","tilescale":0,"mapzoomout":5,"mapzoomin":4,"image-format":"jpg",
   "worldtomap":[4.0,0.0,-2.4492935982947064e-16,-2.4492935982947064e-16,0.0,-4.0,0.0,1.0,0.0]}]},
 {"name":"iso_only","maps":[
  {"type":"HDMapType","name":"surface","prefix":"t","tilescale":0,"mapzoomout":5,"mapzoomin":4,
   "worldtomap":[2.82842712474619,0.0,-2.8284271247461903,-1.4142135623730954,3.4641016151377544,-1.4142135623730951,5.551115123125782e-17,0.9999999999999997,5.551115123125782e-17]}]}
]}`

func flatLayer(t *testing.T) *dynmapLayer {
	t.Helper()
	l, why := newDynmapLayer("https://h/tiles/", "Paralon", dynmapMap{
		Type: "HDMapType", Name: "flat", Prefix: "flat", MapZoomOut: 5, MapZoomIn: 4, ImageFormat: "jpg",
		WorldToMap: []float64{4, 0, -2.4492935982947064e-16, -2.4492935982947064e-16, 0, -4, 0, 1, 0},
	})
	if l == nil {
		t.Fatal(why)
	}
	return l
}

// 瓦片名与真实 dynmap 服务器上存在的文件对照（hdmap.js getTileName 的结果）。
func TestDynmapUpstreamURL(t *testing.T) {
	l := flatLayer(t)
	half := 1 << (dynmapNativeZoom - 1)
	cases := []struct {
		z, col, row int
		want        string
	}{
		{dynmapNativeZoom, 128, 19, "https://h/tiles/Paralon/flat/4_-1/128_-19.jpg"},
		{dynmapNativeZoom, -1, -1, "https://h/tiles/Paralon/flat/-1_0/-1_1.jpg"},
		{dynmapNativeZoom - 1, 3, -2, "https://h/tiles/Paralon/flat/0_0/z_6_4.jpg"},
		{dynmapNativeZoom - 5, 0, 1, "https://h/tiles/Paralon/flat/0_-1/zzzzz_0_-32.jpg"},
	}
	for _, c := range cases {
		h := half >> (dynmapNativeZoom - c.z)
		got, ok := l.UpstreamURL(c.z, c.col+h, c.row+h)
		if !ok || got != c.want {
			t.Errorf("z=%d col=%d row=%d: got %q %v, want %q", c.z, c.col, c.row, got, ok, c.want)
		}
	}
	if _, ok := l.UpstreamURL(dynmapNativeZoom-6, 0, 0); ok {
		t.Error("zoom below mapzoomout should be out of range")
	}
	if _, ok := l.UpstreamURL(dynmapNativeZoom+1, 0, 0); ok {
		t.Error("zoom above native should be out of range")
	}
}

// 游戏坐标经前端 coords.ts 的线性映射落到的瓦片，应等于 dynmap 中包含该方块的瓦片。
func TestDynmapAlignment(t *testing.T) {
	l := flatLayer(t)
	tile := l.Tile()
	ts := float64(tile.TileSize)
	center := ts * float64(int(1)<<dynmapNativeZoom) / 2
	for _, p := range [][2]float64{{0, 0}, {4117, -591}, {-1000.5, 2500.25}, {31.9, -32.1}} {
		px := center + (p[0]+tile.MapOffset[0])*tile.MapScale
		py := center + (p[1]+tile.MapOffset[1])*tile.MapScale
		X, Y := int(px/ts), int(py/ts)
		// dynmap：像素 x = scale·x，像素 y = T + scale·z（Leaflet CRS.Simple 下的行号）。
		col := floorDiv(4*p[0], ts)
		row := floorDiv(ts+4*p[1], ts)
		half := 1 << (dynmapNativeZoom - 1)
		if X-half != col || Y-half != row {
			t.Errorf("game %v: frontend tile (%d,%d) → dynmap (%d,%d), want (%d,%d)", p, X, Y, X-half, Y-half, col, row)
		}
	}
	if tile.MinNativeZoom != dynmapNativeZoom-5 || tile.MinZoom != dynmapNativeZoom-5-2 || tile.MaxZoom != dynmapNativeZoom+4-2 {
		t.Errorf("zoom range = %+v", tile)
	}
}

func floorDiv(a, b float64) int {
	q := a / b
	if q < 0 && float64(int(q)) != q {
		return int(q) - 1
	}
	return int(q)
}

func TestJSEscape(t *testing.T) {
	if got := jsEscape("my world/flat/0_0/z_1_-2.png"); got != "my%20world/flat/0_0/z_1_-2.png" {
		t.Errorf("got %q", got)
	}
	if got := jsEscape("世界"); got != "%E4%B8%96%E7%95%8C" {
		t.Errorf("got %q", got)
	}
}

func newTestUpstream(t *testing.T, configJS string) (*httptest.Server, *int) {
	t.Helper()
	tileHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/map/standalone/config.js":
			if configJS == "" {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, configJS)
		case strings.HasSuffix(r.URL.Path, "dynmap_config.json"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, testDynmapConfig)
		case r.URL.Path == "/map/tiles/Paralon/flat/4_-1/128_-19.jpg",
			r.URL.Path == "/map/up/tiles.php" && r.URL.Query().Get("tile") == "Paralon/flat/4_-1/128_-19.jpg":
			tileHits++
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("JPEGDATA"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &tileHits
}

func newTestService(srcs []config.TileSourceConfig, cacheMB int) *Service {
	cfg := config.TilesConfig{Sources: srcs, CacheMaxMB: cacheMB}
	config.ApplyTilesDefaults(&cfg)
	return New(func() config.TilesConfig { return cfg }, nil, quiet)
}

func TestServiceDiscoverAndProxy(t *testing.T) {
	for _, tc := range []struct{ name, configJS string }{
		{"default paths", ""},
		{"config.js paths", `var config = { url : { configuration: 'standalone/dynmap_config.json?_={timestamp}', tiles: 'up/tiles.php?tile=' } };`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			up, hits := newTestUpstream(t, tc.configJS)
			s := newTestService([]config.TileSourceConfig{{ID: "s2", Type: "dynmap", URL: up.URL + "/map"}}, 64)
			s.refresh(context.Background())

			st := s.Status()
			if len(st.Sources) != 1 || st.Sources[0].Error != "" || len(st.Sources[0].Worlds) != 2 {
				t.Fatalf("status = %+v", st)
			}
			if w := st.Sources[0].Worlds; w[0].Map != "flat" || w[1].Map != "" || w[1].Skip == "" {
				t.Errorf("worlds = %+v", w)
			}

			half := 1 << (dynmapNativeZoom - 1)
			for i := 0; i < 2; i++ {
				tile, err := s.Fetch(context.Background(), "s2", "Paralon", dynmapNativeZoom, 128+half, 19+half)
				if err != nil || string(tile.Data) != "JPEGDATA" || tile.ContentType != "image/jpeg" {
					t.Fatalf("fetch = %q %q %v", tile.Data, tile.ContentType, err)
				}
			}
			if *hits != 1 {
				t.Errorf("upstream hits = %d, want 1 (second fetch cached)", *hits)
			}
			// 上游 404 → 透明 PNG
			tile, err := s.Fetch(context.Background(), "s2", "Paralon", dynmapNativeZoom, half, half)
			if err != nil || tile.ContentType != "image/png" {
				t.Errorf("missing tile = %q %v", tile.ContentType, err)
			}
			// 未发现的世界 / 来源、越界坐标 → ErrNotFound
			for _, f := range []func() error{
				func() error { _, err := s.Fetch(context.Background(), "s2", "iso_only", 14, 0, 0); return err },
				func() error { _, err := s.Fetch(context.Background(), "nope", "Paralon", 14, 0, 0); return err },
				func() error { _, err := s.Fetch(context.Background(), "s2", "Paralon", 3, 8, 0); return err },
			} {
				if err := f(); err != ErrNotFound {
					t.Errorf("err = %v, want ErrNotFound", err)
				}
			}
		})
	}
}

func staticLayer(url string) config.WorldLayerConfig {
	return config.WorldLayerConfig{Static: &config.WorldTileConfig{TileURL: url}}
}

func TestWorldTiles(t *testing.T) {
	up, _ := newTestUpstream(t, "")
	cfg := config.TilesConfig{Sources: []config.TileSourceConfig{
		{ID: "a", Type: "dynmap", URL: up.URL + "/map"},
		{ID: "b", Type: "dynmap", URL: up.URL + "/map"},
	}}
	config.ApplyTilesDefaults(&cfg)
	s := New(func() config.TilesConfig { return cfg }, nil, quiet)
	s.refresh(context.Background())

	// 未保存过：发现的外部地图自动排在最前，按来源顺序
	got := s.WorldTiles()
	if p := got["Paralon"]; p.TileURL != ProxyPath+"/a/Paralon/{z}/{x}/{y}" || p.TileSize != 128 || p.MapScale != 4 {
		t.Errorf("Paralon = %+v (first source should win)", p)
	}
	if _, ok := got["iso_only"]; ok {
		t.Error("world without top-down map should not be listed")
	}
	if _, ok := got["world1"]; !ok {
		t.Error("static world lost")
	}
	auto := EffectiveLayers(cfg, s.state.Load().discovered())["Paralon"]
	if len(auto) != 2 || auto[0].External.Source != "a" || auto[1].External.Source != "b" {
		t.Errorf("auto layers = %+v", auto)
	}

	// 用户排序：静态在前则用静态；外部地图参数各自独立，minZoom 不低于推导下限
	cfg.Worlds = map[string][]config.WorldLayerConfig{
		"Paralon": {
			staticLayer("https://static/{z}/{x}/{y}.png"),
			{External: &config.ExternalLayerConfig{Source: "b", Zoom: 12, Center: []float64{1, 2}, MinZoom: 1, MaxZoom: 20}},
		},
		"iso_only": {staticLayer("https://iso/{z}/{x}/{y}.png")},
	}
	got = s.WorldTiles()
	if got["Paralon"].TileURL != "https://static/{z}/{x}/{y}.png" {
		t.Errorf("user order ignored: %+v", got["Paralon"])
	}
	cfg.Worlds["Paralon"] = []config.WorldLayerConfig{cfg.Worlds["Paralon"][1], cfg.Worlds["Paralon"][0]}
	got = s.WorldTiles()
	if g := got["Paralon"]; g.TileURL != ProxyPath+"/b/Paralon/{z}/{x}/{y}" || g.MapScale != 4 ||
		g.Zoom != 12 || len(g.Center) != 2 || g.MaxZoom != 20 || g.MinZoom != dynmapNativeZoom-7-4 || g.MinNativeZoom != dynmapNativeZoom-5-4 {
		t.Errorf("Paralon = %+v", g)
	}
	// 已保存的列表不含来源 a：a 追加在末尾，不打乱用户顺序
	if l := EffectiveLayers(cfg, s.state.Load().discovered())["Paralon"]; len(l) != 3 || l[2].External.Source != "a" || len(cfg.Worlds["Paralon"]) != 2 {
		t.Errorf("effective = %+v", l)
	}
	if got["iso_only"].TileURL != "https://iso/{z}/{x}/{y}.png" {
		t.Errorf("static fallback lost: %+v", got["iso_only"])
	}
}

// 来源读取失败：该来源的外部地图不再下发，改用下一个底图；代理仍可用上次结果服务旧页面。
func TestDiscoveryFailureFallsBack(t *testing.T) {
	up, _ := newTestUpstream(t, "")
	cfg := config.TilesConfig{
		Sources: []config.TileSourceConfig{{ID: "a", Type: "dynmap", URL: up.URL + "/map"}},
		Worlds: map[string][]config.WorldLayerConfig{"Paralon": {
			{External: &config.ExternalLayerConfig{Source: "a"}},
			staticLayer("https://static/{z}/{x}/{y}.png"),
		}},
	}
	config.ApplyTilesDefaults(&cfg)
	s := New(func() config.TilesConfig { return cfg }, nil, quiet)
	s.refresh(context.Background())
	if g := s.WorldTiles()["Paralon"]; g.TileURL != ProxyPath+"/a/Paralon/{z}/{x}/{y}" {
		t.Fatalf("external not used: %+v", g)
	}
	up.Close()
	s.refresh(context.Background())
	st := s.Status().Sources[0]
	if st.Error == "" || len(st.Worlds) != 2 {
		t.Errorf("status = %+v", st)
	}
	if g := s.WorldTiles()["Paralon"]; g.TileURL != "https://static/{z}/{x}/{y}.png" {
		t.Errorf("should fall back to static: %+v", g)
	}
	if _, err := s.Fetch(context.Background(), "a", "Paralon", 3, 0, 0); err == ErrNotFound {
		t.Error("proxy should still know last good worlds")
	}
}

func TestTileCacheEviction(t *testing.T) {
	c := newTileCache()
	now := time.Now()
	for _, k := range []string{"a", "b", "c"} {
		c.put(&cacheEntry{key: k, data: make([]byte, 10), expires: now.Add(1e9)}, 25)
	}
	if _, _, ok := c.get("a", now); ok {
		t.Error("oldest entry should be evicted")
	}
	if n, b := c.stats(); n != 2 || b != 20 {
		t.Errorf("stats = %d %d", n, b)
	}
	if _, stale, ok := c.get("b", now); !ok || stale {
		t.Error("fresh entry should hit")
	}
	if _, stale, ok := c.get("b", now.Add(2e9)); !ok || !stale {
		t.Error("expired entry should be returned as stale")
	}
}
