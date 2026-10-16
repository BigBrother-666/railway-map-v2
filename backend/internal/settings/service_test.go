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
