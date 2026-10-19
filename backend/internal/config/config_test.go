package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 验证 frontend currencyName 与 route 搜索/联程票默认值都被填充（修复此前 currencyName 漏配）。
func TestFrontendDefaults(t *testing.T) {
	var f FrontendConfig
	ApplyFrontendDefaults(&f)
	if f.CurrencyName != "帕元" {
		t.Errorf("CurrencyName = %q, want 帕元", f.CurrencyName)
	}
	if rd := f.RouteDiagram; rd.MinStationGapPx != 32 || rd.MaxStationGapPx != 60 {
		t.Errorf("station gap = %v/%v, want 32/60", rd.MinStationGapPx, rd.MaxStationGapPx)
	}
	var rt RouteConfig
	ApplyRouteDefaults(&rt)
	if rt.QueryRateLimitPerSecond != 3 {
		t.Errorf("QueryRateLimitPerSecond = %v, want 3", rt.QueryRateLimitPerSecond)
	}
	if rt.MaxDistanceResults != 5 || rt.MaxPriceResults != 5 {
		t.Errorf("max results = %d/%d, want 5/5", rt.MaxDistanceResults, rt.MaxPriceResults)
	}
	if rt.SearchWeightDistance != 0.5 || rt.SearchWeightPrice != 0.5 {
		t.Errorf("weights = %v/%v, want 0.5/0.5", rt.SearchWeightDistance, rt.SearchWeightPrice)
	}
	if rt.MinDirectResults != 1 {
		t.Errorf("MinDirectResults = %d, want 1", rt.MinDirectResults)
	}
	if rt.MaxTransferResults != 3 {
		t.Errorf("MaxTransferResults = %d, want 3", rt.MaxTransferResults)
	}
	if rt.TransferMinImprovement != 0.2 {
		t.Errorf("TransferMinImprovement = %v, want 0.2", rt.TransferMinImprovement)
	}
}

// 旧版 config.yml 中的 frontend / route 段解析为 Legacy（供一次性导入数据库），显式配置原样保留（含显式 0 权重）。
func TestLegacySectionsParsed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	yaml := "frontend:\n" +
		"  currencyName: \"Gold\"\n" +
		"route:\n" +
		"  maxDistanceResults: 8\n" +
		"  searchWeightDistance: 1.0\n" +
		"  searchWeightPrice: 0.0\n" +
		"  transferMinImprovement: 0.0\n"
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Legacy.Frontend == nil || cfg.Legacy.Route == nil {
		t.Fatalf("legacy sections not parsed: %+v", cfg.Legacy)
	}
	f := *cfg.Legacy.Frontend
	ApplyFrontendDefaults(&f)
	if f.CurrencyName != "Gold" {
		t.Errorf("CurrencyName = %q, want Gold", f.CurrencyName)
	}
	rt := *cfg.Legacy.Route
	ApplyRouteDefaults(&rt)
	if rt.MaxDistanceResults != 8 {
		t.Errorf("MaxDistanceResults = %d, want 8", rt.MaxDistanceResults)
	}
	if rt.SearchWeightDistance != 1.0 || rt.SearchWeightPrice != 0.0 {
		t.Errorf("weights = %v/%v, want 1.0/0.0 (显式配置保留)", rt.SearchWeightDistance, rt.SearchWeightPrice)
	}
}

// 没有 frontend / route 段时 Legacy 为 nil（不会触发导入）。
func TestLegacySectionsAbsent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, []byte("server:\n  addr: \":9000\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Legacy.Frontend != nil || cfg.Legacy.Route != nil || cfg.Legacy.Plugin != nil || cfg.Legacy.Realtime != nil {
		t.Errorf("Legacy = %+v, want all nil", cfg.Legacy)
	}
}

// plugin 段只有 sharedToken 时不算旧段；出现已迁移的键才解析为 Legacy.Plugin。realtime 段整段迁移。
func TestLegacyPluginRealtime(t *testing.T) {
	dir := t.TempDir()
	onlyToken := filepath.Join(dir, "a.yml")
	if err := os.WriteFile(onlyToken, []byte("plugin:\n  sharedToken: \"t\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(onlyToken)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Plugin.SharedToken != "t" || cfg.Legacy.Plugin != nil {
		t.Errorf("token=%q legacy=%+v", cfg.Plugin.SharedToken, cfg.Legacy.Plugin)
	}

	full := filepath.Join(dir, "b.yml")
	yml := "plugin:\n  sharedToken: \"t\"\n  heartbeatSeconds: 20\n  configTimeoutSeconds: 7\n" +
		"realtime:\n  trainTimeoutSeconds: 45\n"
	if err := os.WriteFile(full, []byte(yml), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(full)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Legacy.Plugin == nil || cfg.Legacy.Plugin.HeartbeatSeconds != 20 || cfg.Legacy.Plugin.ConfigTimeoutSeconds != 7 {
		t.Fatalf("Legacy.Plugin = %+v", cfg.Legacy.Plugin)
	}
	if cfg.Legacy.Realtime == nil || cfg.Legacy.Realtime.TrainTimeoutSeconds != 45 {
		t.Fatalf("Legacy.Realtime = %+v", cfg.Legacy.Realtime)
	}
	p := *cfg.Legacy.Plugin
	ApplyPluginDefaults(&p)
	if p.PurchaseTimeoutSeconds != 10 || p.PurchaseMinIntervalSeconds != 3 {
		t.Errorf("plugin defaults = %+v", p)
	}
	rt := *cfg.Legacy.Realtime
	ApplyRealtimeDefaults(&rt)
	if rt.ClientSendBuffer != 64 {
		t.Errorf("ClientSendBuffer = %d, want 64", rt.ClientSendBuffer)
	}
}
