package settings

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open("sqlite", "", filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestDefaultsWhenEmpty(t *testing.T) {
	s, err := New(openStore(t), config.LegacySections{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if s.Frontend().CurrencyName != "帕元" || s.Route().ComputeTimeoutMs != 15000 {
		t.Errorf("defaults not applied: %+v %+v", s.Frontend(), s.Route())
	}
	if s.Plugin().HeartbeatSeconds != 15 || s.Realtime().TrainTimeoutSeconds != 30 {
		t.Errorf("defaults not applied: %+v %+v", s.Plugin(), s.Realtime())
	}
}

// 旧 yml 段只在数据库为空时导入一次；之后数据库优先，yml 被忽略。
func TestLegacyImportOnce(t *testing.T) {
	st := openStore(t)
	legacy := config.LegacySections{
		Frontend: &config.FrontendConfig{CurrencyName: "Gold"},
		Route:    &config.RouteConfig{MaxDistanceResults: 8, SearchWeightDistance: 1},
	}
	s, err := New(st, legacy, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if s.Frontend().CurrencyName != "Gold" || s.Route().MaxDistanceResults != 8 {
		t.Fatalf("legacy not imported: %+v", s.Current())
	}
	if s.Route().SearchWeightPrice != 0 {
		t.Errorf("explicit weight lost: %v", s.Route().SearchWeightPrice)
	}
	if s.Current().FrontendMeta.UpdatedBy != "migration" {
		t.Errorf("meta = %+v", s.Current().FrontendMeta)
	}
	legacy.Frontend.CurrencyName = "Silver"
	s2, _ := New(st, legacy, quiet)
	if s2.Frontend().CurrencyName != "Gold" {
		t.Errorf("legacy re-imported: %q", s2.Frontend().CurrencyName)
	}
}

func TestUpdateValidateReset(t *testing.T) {
	st := openStore(t)
	s, _ := New(st, config.LegacySections{}, quiet)
	f := s.Frontend()
	f.ThemeColor = "red"
	f.MapStyle.DimOpacity = 2
	err := s.UpdateFrontend(f, "u1")
	var fe FieldErrors
	if !errors.As(err, &fe) || len(fe) != 2 {
		t.Fatalf("err = %v, want 2 field errors", err)
	}
	f.ThemeColor, f.MapStyle.DimOpacity = "#123456", 0.3
	if err := s.UpdateFrontend(f, "u1"); err != nil {
		t.Fatal(err)
	}
	if s.Frontend().ThemeColor != "#123456" || s.Current().FrontendMeta.UpdatedBy != "u1" {
		t.Errorf("update not applied: %+v", s.Current().FrontendMeta)
	}
	// 重新加载仍是保存后的值
	s2, _ := New(st, config.LegacySections{}, quiet)
	if s2.Frontend().ThemeColor != "#123456" {
		t.Errorf("not persisted: %q", s2.Frontend().ThemeColor)
	}
	if err := s.Reset(SectionFrontend); err != nil {
		t.Fatal(err)
	}
	if s.Frontend().ThemeColor != "#ffd400" {
		t.Errorf("reset failed: %q", s.Frontend().ThemeColor)
	}
	if err := s.Reset("nope"); !errors.Is(err, ErrUnknownSection) {
		t.Errorf("unknown section err = %v", err)
	}
}

func TestValidateRoute(t *testing.T) {
	if errs := ValidateRoute(config.RouteConfig{SearchWeightDistance: 1.5, ComputeTimeoutMs: -1}); len(errs) != 2 {
		t.Errorf("errs = %v", errs)
	}
	// max*Results <= 0 表示不限制，合法
	if errs := ValidateRoute(config.RouteConfig{MaxDistanceResults: -1}); len(errs) != 0 {
		t.Errorf("errs = %v", errs)
	}
}

// plugin / realtime 段：旧 yml 导入一次、校验、保存、重置。
func TestPluginRealtimeSections(t *testing.T) {
	st := openStore(t)
	legacy := config.LegacySections{
		Plugin:   &config.PluginSettings{HeartbeatSeconds: 20},
		Realtime: &config.RealtimeConfig{TrainTimeoutSeconds: 45},
	}
	s, err := New(st, legacy, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if s.Plugin().HeartbeatSeconds != 20 || s.Plugin().PurchaseTimeoutSeconds != 10 || s.Realtime().TrainTimeoutSeconds != 45 {
		t.Fatalf("legacy not imported: %+v %+v", s.Plugin(), s.Realtime())
	}
	if s.Current().PluginMeta.UpdatedBy != "migration" || s.Current().RealtimeMeta.UpdatedBy != "migration" {
		t.Errorf("meta = %+v %+v", s.Current().PluginMeta, s.Current().RealtimeMeta)
	}

	var fe FieldErrors
	if err := s.UpdatePlugin(config.PluginSettings{HeartbeatSeconds: -1}, "u1"); !errors.As(err, &fe) || len(fe) != 1 {
		t.Fatalf("err = %v, want 1 field error", err)
	}
	if err := s.UpdateRealtime(config.RealtimeConfig{ClientSendBuffer: 99999}, "u1"); !errors.As(err, &fe) || len(fe) != 1 {
		t.Fatalf("err = %v, want 1 field error", err)
	}
	if err := s.UpdatePlugin(config.PluginSettings{PurchaseMinIntervalSeconds: 5}, "u1"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRealtime(config.RealtimeConfig{ClientSendBuffer: 128}, "u1"); err != nil {
		t.Fatal(err)
	}
	s2, _ := New(st, config.LegacySections{}, quiet)
	if s2.Plugin().PurchaseMinIntervalSeconds != 5 || s2.Plugin().HeartbeatSeconds != 15 || s2.Realtime().ClientSendBuffer != 128 {
		t.Errorf("not persisted: %+v %+v", s2.Plugin(), s2.Realtime())
	}

	if err := s.Reset(SectionPlugin); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(SectionRealtime); err != nil {
		t.Fatal(err)
	}
	if s.Plugin().PurchaseMinIntervalSeconds != 3 || s.Realtime().ClientSendBuffer != 64 {
		t.Errorf("reset failed: %+v %+v", s.Plugin(), s.Realtime())
	}
}

func TestTilesSection(t *testing.T) {
	s, err := New(openStore(t), config.LegacySections{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tiles().CacheMaxMB != 64 || s.Tiles().Sources == nil || s.Tiles().Worlds["world1"][0].Static.Zoom != 14 {
		t.Errorf("defaults not applied: %+v", s.Tiles())
	}
	tooMany := 7
	bad := config.TilesConfig{Sources: []config.TileSourceConfig{
		{ID: "a", URL: "ftp://x"}, {ID: "a", Type: "bluemap", URL: "https://x/"}, {ID: "b c", URL: "https://x/"},
	}, CacheMaxMB: -1, Worlds: map[string][]config.WorldLayerConfig{"world1": {
		{External: &config.ExternalLayerConfig{Source: "a", SynthZoomLevels: &tooMany, SynthMarginBlocks: -1}},
		{External: &config.ExternalLayerConfig{Source: "a"}},    // 重复来源
		{External: &config.ExternalLayerConfig{Source: "nope"}}, // 不存在的来源
		{}, // 两者都空
		{Static: &config.WorldTileConfig{Scheme: "x"}},
	}}}
	var fe FieldErrors
	if err := s.UpdateTiles(bad, "u"); !errors.As(err, &fe) || len(fe) != 11 {
		t.Errorf("err = %v (%d)", err, len(fe))
	}
	ok := config.TilesConfig{Sources: []config.TileSourceConfig{{ID: "s2", URL: "https://map.example.com/dynmap/"}}, CacheMaxMB: 128}
	if err := s.UpdateTiles(ok, "u"); err != nil {
		t.Fatal(err)
	}
	if s.Tiles().Sources[0].Type != "dynmap" || s.Tiles().CacheMaxMB != 128 || s.Current().TilesMeta.UpdatedBy != "u" {
		t.Errorf("saved = %+v %+v", s.Tiles(), s.Current().TilesMeta)
	}
	// 配置了外部来源后 defaultWorld 可以是未写进 worlds 的世界
	f := s.Frontend()
	f.DefaultWorld = "Paralon"
	if err := s.UpdateFrontend(f, "u"); err != nil {
		t.Errorf("defaultWorld from tile source rejected: %v", err)
	}
	// 去掉外部来源后 worlds 必须包含默认世界
	only := config.TilesConfig{Worlds: map[string][]config.WorldLayerConfig{"world1": {{Static: &config.WorldTileConfig{}}}}}
	if err := s.UpdateTiles(only, "u"); !errors.As(err, &fe) {
		t.Errorf("missing default world accepted: %v", err)
	}
	if err := s.Reset(SectionTiles); err != nil || len(s.Tiles().Sources) != 0 {
		t.Errorf("reset: %v %+v", err, s.Tiles())
	}
}

// 旧版每世界单一配置（worldTiles）转为底图列表，保持「外部地图优先，其次静态瓦片」。
func TestLegacyWorldTilesConversion(t *testing.T) {
	st := openStore(t)
	if err := st.SaveSetting(SectionTiles, []byte(`{"sources":[{"id":"s2","type":"dynmap","url":"https://m/"}],
		"worldTiles":{"Paralon":{"zoom":7,"minZoom":5,"synthZoomLevels":2},"other":{"tileUrl":"https://o/{z}/{x}/{y}.png","mapScale":2}}}`), "u"); err != nil {
		t.Fatal(err)
	}
	s, err := New(st, config.LegacySections{}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	w := s.Tiles().Worlds
	if p := w["Paralon"]; len(p) != 1 || p[0].External == nil || p[0].External.Source != "s2" || p[0].External.Zoom != 7 ||
		p[0].External.MinZoom != 5 || p[0].External.SynthLevels() != 2 {
		t.Errorf("Paralon = %+v", p)
	}
	if o := w["other"]; len(o) != 2 || o[0].External == nil || o[1].Static == nil || o[1].Static.MapScale != 2 {
		t.Errorf("other = %+v", o)
	}
	if s.Tiles().WorldTiles != nil {
		t.Error("legacy field should be cleared")
	}
}

// 旧版数据库 frontend 段里的 worldTiles 移到 tiles 段（优先于 config.yml），只迁移一次。
func TestWorldTilesMigration(t *testing.T) {
	st := openStore(t)
	if err := st.SaveSetting(SectionFrontend, []byte(`{"currencyName":"Gold","worldTiles":{"w":{"tileUrl":"https://db/{z}/{x}/{y}.png"}}}`), "old"); err != nil {
		t.Fatal(err)
	}
	legacy := config.LegacySections{Tiles: &config.TilesConfig{WorldTiles: map[string]config.LegacyWorldTileConfig{"yml": {}}}}
	s, err := New(st, legacy, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if w := s.Tiles().Worlds; len(w) != 1 || w["w"][0].Static.TileURL != "https://db/{z}/{x}/{y}.png" {
		t.Fatalf("worlds = %+v", w)
	}
	if s.Frontend().CurrencyName != "Gold" || s.Current().TilesMeta.UpdatedBy != "migration" {
		t.Errorf("frontend = %+v meta = %+v", s.Frontend(), s.Current().TilesMeta)
	}
	saved := config.TilesConfig{Worlds: map[string][]config.WorldLayerConfig{"world1": {{Static: &config.WorldTileConfig{Zoom: 3}}}}}
	if err := s.UpdateTiles(saved, "u"); err != nil {
		t.Fatal(err)
	}
	s2, _ := New(st, legacy, quiet)
	if w := s2.Tiles().Worlds; len(w) != 1 || w["world1"][0].Static.Zoom != 3 {
		t.Errorf("migrated again: %+v", w)
	}
}

// 无数据库记录时导入 config.yml 中的 frontend.worldTiles。
func TestWorldTilesLegacyYAML(t *testing.T) {
	legacy := config.LegacySections{Tiles: &config.TilesConfig{WorldTiles: map[string]config.LegacyWorldTileConfig{
		"yml": {WorldTileConfig: config.WorldTileConfig{Zoom: 5}},
	}}}
	s, err := New(openStore(t), legacy, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if w := s.Tiles().Worlds["yml"]; len(w) != 1 || w[0].Static.Zoom != 5 {
		t.Errorf("worlds = %+v", s.Tiles().Worlds)
	}
}
