package routing

import "sort"

// Candidate 是一个候选的排序输入：原始下标 + 总距离 + 总票价。
type Candidate struct {
	Index    int
	Distance float64
	Price    float64
}

// Rank 对候选做「距离前 N ∪ 票价前 M → 归一化加权排序」，返回排序后的原始下标序列（已去重）。
// 复刻插件 TicketRanker：取「距离最近」前 N 与「票价最低」前 M，合并去重作为展示集；展示集内距离、
// 票价各归一化到 [0,1]（按集合内 min/max 线性缩放）；weight = wDistance*归一化距离 + wPrice*归一化票价，
// weight 越小越靠前。
func Rank(candidates []Candidate, maxByDistance, maxByPrice int, wDistance, wPrice float64) []int {
	if len(candidates) == 0 {
		return nil
	}

	byDistance := append([]Candidate(nil), candidates...)
	sort.Slice(byDistance, func(i, j int) bool { return byDistance[i].Distance < byDistance[j].Distance })
	byPrice := append([]Candidate(nil), candidates...)
	sort.Slice(byPrice, func(i, j int) bool { return byPrice[i].Price < byPrice[j].Price })

	// 合并去重（按原始下标），保持「先距离后票价」的加入顺序
	selected := make(map[int]struct{})
	var order []int
	addTop(selected, &order, byDistance, maxByDistance)
	addTop(selected, &order, byPrice, maxByPrice)

	return sortByWeight(order, candidates, wDistance, wPrice)
}

// RankWithMinDirect 在 Rank 基础上加「直达票兜底」：若排序结果里没有任何直达票（全是联程票）且
// minDirect>0，则把最优的 minDirect 条直达票（按同一权重公式排序）补到结果最前。直达 / 联程票通过
// 下标区分：index < directCount 为直达（与调用方拼装候选时的下标布局一致）。
func RankWithMinDirect(candidates []Candidate, directCount, maxByDistance, maxByPrice int, wDistance, wPrice float64, minDirect int) []int {
	order := Rank(candidates, maxByDistance, maxByPrice, wDistance, wPrice)
	if minDirect <= 0 || directCount <= 0 {
		return order
	}
	// 结果里已有直达票则无需兜底
	for _, idx := range order {
		if idx < directCount {
			return order
		}
	}
	// 取最优的 minDirect 条直达票（按同一权重排序），补到最前
	directIndices := make([]int, directCount)
	for i := 0; i < directCount; i++ {
		directIndices[i] = i
	}
	bestDirect := sortByWeight(directIndices, candidates, wDistance, wPrice)
	take := minDirect
	if take > len(bestDirect) {
		take = len(bestDirect)
	}
	return append(append([]int{}, bestDirect[:take]...), order...)
}

// sortByWeight 把给定下标集合按归一化加权公式排序（范围取自该集合内 min/max，span 为 0 时归一化取 0 防除零）。
func sortByWeight(indices []int, candidates []Candidate, wDistance, wPrice float64) []int {
	minDist, maxDist := infinity, -infinity
	minPrice, maxPrice := infinity, -infinity
	for _, idx := range indices {
		c := candidates[idx]
		if c.Distance < minDist {
			minDist = c.Distance
		}
		if c.Distance > maxDist {
			maxDist = c.Distance
		}
		if c.Price < minPrice {
			minPrice = c.Price
		}
		if c.Price > maxPrice {
			maxPrice = c.Price
		}
	}
	distSpan := maxDist - minDist
	priceSpan := maxPrice - minPrice
	weightOf := func(idx int) float64 {
		c := candidates[idx]
		normDist := 0.0
		if distSpan > 0 {
			normDist = (c.Distance - minDist) / distSpan
		}
		normPrice := 0.0
		if priceSpan > 0 {
			normPrice = (c.Price - minPrice) / priceSpan
		}
		return wDistance*normDist + wPrice*normPrice
	}
	ret := append([]int(nil), indices...)
	sort.SliceStable(ret, func(i, j int) bool { return weightOf(ret[i]) < weightOf(ret[j]) })
	return ret
}

// addTop 把已排序列表的前 limit 条的原始下标加入 selected/order（limit<=0 表示全部）。
func addTop(selected map[int]struct{}, order *[]int, sorted []Candidate, limit int) {
	count := len(sorted)
	if limit > 0 && limit < count {
		count = limit
	}
	for i := 0; i < count; i++ {
		idx := sorted[i].Index
		if _, ok := selected[idx]; !ok {
			selected[idx] = struct{}{}
			*order = append(*order, idx)
		}
	}
}
