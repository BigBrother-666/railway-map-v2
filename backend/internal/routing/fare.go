package routing

import (
	"math"

	"railway-map-backend/internal/model"
)

// System 是票价估算所需的最小铁路系统信息（对齐前端 RailwaySystem）。
type System struct {
	ID         string
	Name       string
	PricePerKm float64
	HasPrice   bool // PricePerKm 是否显式配置；false 时用 globalPricePerKm
}

// EstimateFare 按各段所属铁路系统的 pricePerKm × 段公里数累加（未登录时不计成员免票，仅供展示，
// 最终以购票返回为准）。
func EstimateFare(path model.RoutePath, systems map[string]System, globalPricePerKm float64) float64 {
	details := FareDetails(path, systems, globalPricePerKm)
	total := 0.0
	for _, d := range details {
		total += d.Price
	}
	return round2(total)
}

// FareDetails 按系统汇总每段的距离与票价明细。
func FareDetails(path model.RoutePath, systems map[string]System, globalPricePerKm float64) []model.FareDetail {
	bySystem := make(map[string]*model.FareDetail)
	var order []string
	for _, seg := range path.Segments {
		systemID := seg.SystemID
		if systemID == "" {
			systemID = "__contact__"
		}
		sys, hasSys := systems[seg.SystemID]
		rate := globalPricePerKm
		if hasSys && sys.HasPrice {
			rate = sys.PricePerKm
		}
		if existing, ok := bySystem[systemID]; ok {
			existing.Distance += seg.Distance
			existing.Price += seg.Distance * rate
		} else {
			name := "联络线"
			if seg.SystemID != "" {
				name = seg.SystemID
			}
			if hasSys && sys.Name != "" {
				name = sys.Name
			}
			bySystem[systemID] = &model.FareDetail{
				SystemID:   systemID,
				SystemName: name,
				Distance:   seg.Distance,
				Price:      seg.Distance * rate,
				Rate:       rate,
			}
			order = append(order, systemID)
		}
	}
	ret := make([]model.FareDetail, 0, len(order))
	for _, id := range order {
		d := *bySystem[id]
		d.Distance = round2(d.Distance)
		d.Price = round2(d.Price)
		ret = append(ret, d)
	}
	return ret
}

// MergeFareDetails 合并多段（联程票各段）的收费详情：按铁路系统累加距离与价格（复刻插件 ThroughTicket
// 底部合并各段各系统距离）。
func MergeFareDetails(details [][]model.FareDetail) []model.FareDetail {
	bySystem := make(map[string]*model.FareDetail)
	var order []string
	for _, legDetails := range details {
		for _, d := range legDetails {
			if existing, ok := bySystem[d.SystemID]; ok {
				existing.Distance += d.Distance
				existing.Price += d.Price
			} else {
				copy := d
				bySystem[d.SystemID] = &copy
				order = append(order, d.SystemID)
			}
		}
	}
	ret := make([]model.FareDetail, 0, len(order))
	for _, id := range order {
		d := *bySystem[id]
		d.Distance = round2(d.Distance)
		d.Price = round2(d.Price)
		ret = append(ret, d)
	}
	return ret
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
