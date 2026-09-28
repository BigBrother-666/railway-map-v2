package routing

import (
	"reflect"
	"testing"
)

func TestRank_MergesTopDistanceAndTopPrice(t *testing.T) {
	// c0 最短最贵，c1 中等，c2 最长最便宜
	cands := []Candidate{
		{Index: 0, Distance: 10, Price: 30},
		{Index: 1, Distance: 20, Price: 20},
		{Index: 2, Distance: 30, Price: 10},
	}
	// 距离前 1 = c0，票价前 1 = c2；合并 {c0,c2}，权重 0.5/0.5 归一化后并列，保持加入顺序 c0,c2
	order := Rank(cands, 1, 1, 0.5, 0.5)
	if !reflect.DeepEqual(order, []int{0, 2}) {
		t.Errorf("order = %v, want [0 2]", order)
	}
}

func TestRank_MaxByDistanceZeroMeansUnlimited(t *testing.T) {
	cands := []Candidate{
		{Index: 0, Distance: 10, Price: 30},
		{Index: 1, Distance: 20, Price: 20},
		{Index: 2, Distance: 30, Price: 10},
	}
	order := Rank(cands, 0, 0, 1, 0) // 只看距离
	if !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Errorf("order = %v, want [0 1 2]", order)
	}
}

func TestRank_EmptyCandidatesReturnsEmpty(t *testing.T) {
	if order := Rank(nil, 5, 5, 0.5, 0.5); len(order) != 0 {
		t.Errorf("order = %v, want empty", order)
	}
}

func TestRankWithMinDirect_BackfillsBestDirectWhenAllThrough(t *testing.T) {
	// index<2 为直达，index>=2 为联程票。联程票距离/票价都更优，会挤掉直达
	cands := []Candidate{
		{Index: 0, Distance: 100, Price: 100}, // 直达（差）
		{Index: 1, Distance: 90, Price: 90},   // 直达（较好）
		{Index: 2, Distance: 10, Price: 10},   // 联程票
		{Index: 3, Distance: 12, Price: 12},   // 联程票
	}
	order := RankWithMinDirect(cands, 2, 1, 1, 0.5, 0.5, 1)
	if len(order) == 0 || order[0] != 1 {
		t.Errorf("order[0] = %v, want 1 (最优直达兜底)", order)
	}
	found := false
	for _, idx := range order {
		if idx == 2 {
			found = true
		}
	}
	if !found {
		t.Errorf("order %v should contain index 2", order)
	}
}

func TestRankWithMinDirect_NoBackfillWhenDirectAlreadyPresent(t *testing.T) {
	cands := []Candidate{
		{Index: 0, Distance: 10, Price: 10}, // 直达且最优
		{Index: 1, Distance: 50, Price: 50}, // 联程票
	}
	order := RankWithMinDirect(cands, 1, 5, 5, 0.5, 0.5, 1)
	if len(order) == 0 || order[0] != 0 {
		t.Errorf("order[0] = %v, want 0", order)
	}
	count := 0
	for _, idx := range order {
		if idx == 0 {
			count++
		}
	}
	if count != 1 {
		t.Errorf("index 0 appears %d times, want 1 (无重复兜底)", count)
	}
}

func TestRankWithMinDirect_NoBackfillWhenMinDirectNonPositive(t *testing.T) {
	cands := []Candidate{
		{Index: 0, Distance: 100, Price: 100},
		{Index: 1, Distance: 10, Price: 10},
	}
	order := RankWithMinDirect(cands, 1, 1, 1, 1, 0, 0)
	// 距离前 1 = index1（联程票），无兜底，直达不入选
	if !reflect.DeepEqual(order, []int{1}) {
		t.Errorf("order = %v, want [1]", order)
	}
}
