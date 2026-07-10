package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 写一个最小 yaml，验证 frontend 搜索/联程票默认值与 currencyName 都被填充（修复此前 currencyName 漏配）。
func TestFrontendDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte("server:\n  addr: \":9000\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	f := cfg.Frontend
	if f.CurrencyName != "帕元" {
		t.Errorf("CurrencyName = %q, want 帕元", f.CurrencyName)
	}
	if f.MaxDistanceResults != 5 || f.MaxPriceResults != 5 {
		t.Errorf("max results = %d/%d, want 5/5", f.MaxDistanceResults, f.MaxPriceResults)
	}
	if f.SearchWeightDistance != 0.5 || f.SearchWeightPrice != 0.5 {
		t.Errorf("weights = %v/%v, want 0.5/0.5", f.SearchWeightDistance, f.SearchWeightPrice)
	}
	if f.MinDirectResults != 1 {
		t.Errorf("MinDirectResults = %d, want 1", f.MinDirectResults)
	}
	if f.MaxTransferResults != 3 || f.MaxTransferCandidates != 30 {
		t.Errorf("transfer caps = %d/%d, want 3/30", f.MaxTransferResults, f.MaxTransferCandidates)
	}
	if f.TransferMinImprovement != 0.2 {
		t.Errorf("TransferMinImprovement = %v, want 0.2", f.TransferMinImprovement)
	}
}

// 显式配置应保留，不被默认值覆盖（含 <=0 允许值与显式 0 权重）。
func TestFrontendExplicitValuesKept(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	yaml := "frontend:\n" +
		"  currencyName: \"Gold\"\n" +
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
	f := cfg.Frontend
	if f.CurrencyName != "Gold" {
		t.Errorf("CurrencyName = %q, want Gold", f.CurrencyName)
	}
	if f.MaxDistanceResults != 8 {
		t.Errorf("MaxDistanceResults = %d, want 8", f.MaxDistanceResults)
	}
	if f.SearchWeightDistance != 1.0 || f.SearchWeightPrice != 0.0 {
		t.Errorf("weights = %v/%v, want 1.0/0.0 (显式配置保留)", f.SearchWeightDistance, f.SearchWeightPrice)
	}
}
