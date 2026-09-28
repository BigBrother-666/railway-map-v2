package routing

import (
	"context"

	"railway-map-backend/internal/config"
	"railway-map-backend/internal/model"
)

// ComputeInput 是寻路所需的输入（对齐前端 ComputeInput）。
type ComputeInput struct {
	StartStation string
	EndStation   string
	Systems      map[string]System
	// IsReverse 判断 (lineId, stationName) 是否为折返站，寻路时跳过。
	IsReverse ReversePredicate
	Cfg       config.RouteConfig
	// GlobalPricePerKm 是系统未显式配置票价时的默认每公里价格（对齐 frontend.defaultPricePerKm）。
	GlobalPricePerKm float64
}

// ComputeCandidates 纯寻路：给定图与起终点，返回排序后的候选路线（直达 + 联程票）。
// 复刻前端 routing/compute.ts computeCandidates。
func ComputeCandidates(ctx context.Context, g *Graph, input ComputeInput) []model.RoutePath {
	cfg := input.Cfg
	globalRate := input.GlobalPricePerKm
	withFare := func(p model.RoutePath) model.RoutePath {
		details := FareDetails(p, input.Systems, globalRate)
		p.FareDetails = details
		p.EstimatedFare = EstimateFare(withFareDetails(p, details), input.Systems, globalRate)
		return p
	}

	// 直达候选池：覆盖「距离前 N ∪ 票价前 M」，两者之和作上限；任一不限(<=0)时退回不限条数(0)。
	directPool := 0
	if cfg.MaxDistanceResults > 0 && cfg.MaxPriceResults > 0 {
		directPool = cfg.MaxDistanceResults + cfg.MaxPriceResults
	}
	directRaw := FindByStation(ctx, g, input.StartStation, input.EndStation, directPool, input.IsReverse)
	directCandidates := make([]model.RoutePath, len(directRaw))
	for i, p := range directRaw {
		p = withFare(p)
		p.Kind = "direct"
		p.ExpressRoute = true
		directCandidates[i] = p
	}

	// 联程票候选：限方案数（换乘站用站名级缩合矩阵枚举全部，不再截断候选）
	journeys := FindTransferJourneys(ctx, g, input.StartStation, input.EndStation, cfg.MaxTransferResults, cfg.TransferMinImprovement, input.IsReverse)
	throughCandidates := make([]model.RoutePath, 0, len(journeys))
	for _, j := range journeys {
		legs := make([]model.RoutePath, len(j.Legs))
		legFareDetails := make([][]model.FareDetail, len(j.Legs))
		for i, leg := range j.Legs {
			legs[i] = withFare(leg)
			legs[i].ExpressRoute = true
			legFareDetails[i] = legs[i].FareDetails
		}
		merged := MergeFareDetails(legFareDetails)
		totalFare := 0.0
		for _, l := range legs {
			totalFare += l.EstimatedFare
		}
		totalFare = round2(totalFare)
		rep := legs[0]
		rep.Kind = "through"
		rep.Distance = j.TotalDistance
		rep.EstimatedFare = totalFare
		rep.FareDetails = merged
		rep.ExpressRoute = true
		rep.Journey = &model.JourneyPlan{
			Legs:             legs,
			TransferStations: j.TransferStations,
			TotalDistance:    j.TotalDistance,
			TotalFare:        totalFare,
			FareDetails:      merged,
		}
		throughCandidates = append(throughCandidates, rep)
	}

	// 汇总排序候选：直达在前、联程票在后（下标空间连续）
	all := append(append([]model.RoutePath{}, directCandidates...), throughCandidates...)
	rankInput := make([]Candidate, len(all))
	for i, p := range all {
		rankInput[i] = Candidate{Index: i, Distance: p.Distance, Price: p.EstimatedFare}
	}
	order := RankWithMinDirect(
		rankInput,
		len(directCandidates),
		cfg.MaxDistanceResults,
		cfg.MaxPriceResults,
		cfg.SearchWeightDistance,
		cfg.SearchWeightPrice,
		cfg.MinDirectResults,
	)
	ret := make([]model.RoutePath, len(order))
	for i, idx := range order {
		ret[i] = all[idx]
	}
	return ret
}

func withFareDetails(p model.RoutePath, details []model.FareDetail) model.RoutePath {
	p.FareDetails = details
	return p
}
