package store

import (
	"database/sql"
	"strings"
	"time"
)

// PlayerStats 是玩家个人统计（个人中心概览）。
type PlayerStats struct {
	RideCount        int          `json:"rideCount"`
	TotalKm          float64      `json:"totalKm"`
	TotalFare        float64      `json:"totalFare"`
	TicketCount      int          `json:"ticketCount"`    // 购票次数（插件营收事件，含游戏内与网页）
	TicketSpent      float64      `json:"ticketSpent"`    // 购票花费
	WebTicketCount   int          `json:"webTicketCount"` // 其中网页购票成功次数
	CardChargeCount  int          `json:"cardChargeCount"`
	CardChargeAmount float64      `json:"cardChargeAmount"`
	CardRideCount    int          `json:"cardRideCount"` // 刷卡乘车次数（卡内余额支付，不计入购票）
	CardRideSpent    float64      `json:"cardRideSpent"`
	VisitedStations  int          `json:"visitedStations"`
	FirstRideAt      int64        `json:"firstRideAt,omitempty"`
	LongestRide      *LongestRide `json:"longestRide,omitempty"`
	FavoriteLines    []CountItem  `json:"favoriteLines"` // key = lineId
	TopStations      []CountItem  `json:"topStations"`   // key = 车站名
	DailyRides       []DayValue   `json:"dailyRides"`    // 近 30 天每日乘车次数
}

// LongestRide 是最长一次行程。
type LongestRide struct {
	StartStation string  `json:"startStation"`
	EndStation   string  `json:"endStation"`
	Distance     float64 `json:"distance"`
	EndedAt      int64   `json:"endedAt"`
}

// CountItem 是排行项。
type CountItem struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// DayValue 是按日聚合的一个点（Day 为当天 00:00 本地时间的毫秒时间戳）。
type DayValue struct {
	Day   int64   `json:"day"`
	Value float64 `json:"value"`
}

// dayBuckets 生成最近 days 天（含今天）的零值序列及 day → 下标映射。
func dayBuckets(days int, now time.Time) ([]DayValue, map[int64]int, int64) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	out := make([]DayValue, days)
	idx := make(map[int64]int, days)
	for i := 0; i < days; i++ {
		d := today.AddDate(0, 0, i-days+1).UnixMilli()
		out[i] = DayValue{Day: d}
		idx[d] = i
	}
	return out, idx, out[0].Day
}

func dayOf(ms int64) int64 {
	t := time.UnixMilli(ms)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()).UnixMilli()
}

// PlayerStats 统计某玩家的乘车 / 购票数据。
func (s *Store) PlayerStats(uuid string) (PlayerStats, error) {
	var st PlayerStats
	var firstRide sql.NullInt64
	if err := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(distance), 0), COALESCE(SUM(paid_fare), 0), MIN(started_at)
		 FROM ride_history WHERE player_uuid = ?`, uuid,
	).Scan(&st.RideCount, &st.TotalKm, &st.TotalFare, &firstRide); err != nil {
		return st, err
	}
	st.TotalKm, st.TotalFare, st.FirstRideAt = round2(st.TotalKm), round2(st.TotalFare), firstRide.Int64

	var lr LongestRide
	err := s.db.QueryRow(
		`SELECT start_station, end_station, distance, ended_at FROM ride_history
		 WHERE player_uuid = ? ORDER BY distance DESC, ended_at DESC LIMIT 1`, uuid,
	).Scan(&lr.StartStation, &lr.EndStation, &lr.Distance, &lr.EndedAt)
	if err == nil && lr.Distance > 0 {
		st.LongestRide = &lr
	} else if err != nil && err != sql.ErrNoRows {
		return st, err
	}

	rows, err := s.db.Query(
		`SELECT type, COUNT(*), COALESCE(SUM(amount), 0) FROM revenue_events WHERE player_uuid = ? GROUP BY type`, uuid)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var typ string
		var n int
		var amount float64
		if err := rows.Scan(&typ, &n, &amount); err != nil {
			rows.Close()
			return st, err
		}
		switch typ {
		case "ticket":
			st.TicketCount, st.TicketSpent = n, round2(amount)
		case "card_charge":
			st.CardChargeCount, st.CardChargeAmount = n, round2(amount)
		case "card_ride":
			st.CardRideCount, st.CardRideSpent = n, round2(amount)
		}
	}
	rows.Close()
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM purchase_log WHERE player_uuid = ? AND success = 1`, uuid,
	).Scan(&st.WebTicketCount); err != nil {
		return st, err
	}

	if err := s.db.QueryRow(
		`SELECT COUNT(DISTINCT e.station_name) FROM ride_events e
		 JOIN ride_event_players p ON p.event_id = e.id
		 WHERE p.player_uuid = ? AND e.station_name IS NOT NULL AND e.station_name <> ''`, uuid,
	).Scan(&st.VisitedStations); err != nil {
		return st, err
	}

	// 一次乘车（同一列车）在同一线路 / 车站只计一次
	if st.FavoriteLines, err = s.topCounts(
		`SELECT e.line_id, COUNT(DISTINCT e.train_id) AS n FROM ride_events e
		 JOIN ride_event_players p ON p.event_id = e.id
		 WHERE p.player_uuid = ? AND e.line_id IS NOT NULL AND e.line_id <> ''
		 GROUP BY e.line_id ORDER BY n DESC LIMIT 3`, uuid); err != nil {
		return st, err
	}
	if st.TopStations, err = s.topCounts(
		`SELECT e.station_name, COUNT(DISTINCT e.train_id) AS n FROM ride_events e
		 JOIN ride_event_players p ON p.event_id = e.id
		 WHERE p.player_uuid = ? AND e.station_name IS NOT NULL AND e.station_name <> ''
		 GROUP BY e.station_name ORDER BY n DESC LIMIT 5`, uuid); err != nil {
		return st, err
	}

	buckets, idx, since := dayBuckets(30, time.Now())
	rows, err = s.db.Query(`SELECT ended_at FROM ride_history WHERE player_uuid = ? AND ended_at >= ?`, uuid, since)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var ended int64
		if err := rows.Scan(&ended); err != nil {
			return st, err
		}
		if i, ok := idx[dayOf(ended)]; ok {
			buckets[i].Value++
		}
	}
	st.DailyRides = buckets
	return st, rows.Err()
}

func (s *Store) topCounts(query string, args ...any) ([]CountItem, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CountItem{}
	for rows.Next() {
		var it CountItem
		if err := rows.Scan(&it.Key, &it.Count); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SystemDailyRevenue 返回某系统最近 days 天每日分配营收。
func (s *Store) SystemDailyRevenue(systemID string, days int) ([]DayValue, error) {
	buckets, idx, since := dayBuckets(days, time.Now())
	rows, err := s.db.Query(
		`SELECT created_at, amount FROM revenue_allocations WHERE system_id = ? AND created_at >= ?`, systemID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var at int64
		var amount float64
		if err := rows.Scan(&at, &amount); err != nil {
			return nil, err
		}
		if i, ok := idx[dayOf(at)]; ok {
			buckets[i].Value += amount
		}
	}
	for i := range buckets {
		buckets[i].Value = round2(buckets[i].Value)
	}
	return buckets, rows.Err()
}

// LinesDailyRidership 返回一组线路最近 days 天每日客流（同一天内同一玩家乘同一列车只计一人次）。
func (s *Store) LinesDailyRidership(lineIDs []string, days int) ([]DayValue, error) {
	buckets, idx, since := dayBuckets(days, time.Now())
	if len(lineIDs) == 0 {
		return buckets, nil
	}
	args := []any{since}
	for _, id := range lineIDs {
		args = append(args, id)
	}
	rows, err := s.db.Query(
		`SELECT DISTINCT e.train_id, p.player_uuid, e.arrived_at FROM ride_events e
		 JOIN ride_event_players p ON p.event_id = e.id
		 WHERE e.arrived_at >= ? AND e.line_id IN (?`+strings.Repeat(", ?", len(lineIDs)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	for rows.Next() {
		var trainID, player string
		var at int64
		if err := rows.Scan(&trainID, &player, &at); err != nil {
			return nil, err
		}
		day := dayOf(at)
		key := trainID + "|" + player + "|" + time.UnixMilli(day).Format("20060102")
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if i, ok := idx[day]; ok {
			buckets[i].Value++
		}
	}
	return buckets, rows.Err()
}
