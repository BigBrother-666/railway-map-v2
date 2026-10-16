package store

import (
	"testing"
	"time"

	"railway-map-backend/internal/model"
)

// 带 eventId 的乘车事件重复补发只落库一次，并同步写入 ride_event_players。
func TestRideEventDedupByEventID(t *testing.T) {
	s := openTestStore(t)
	ev := model.RideEventData{
		EventID: "ev-1", TrainID: "t1", NodeID: "n1", StationName: "A", LineID: "L1",
		Passengers: []model.Player{{UUID: "u1", Name: "Steve"}}, ArrivedAt: 1000,
	}
	for i := 0; i < 2; i++ {
		if err := s.RecordRideEvent(ev); err != nil {
			t.Fatalf("record #%d: %v", i, err)
		}
	}
	var events, players int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ride_events`).Scan(&events)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ride_event_players`).Scan(&players)
	if events != 1 || players != 1 {
		t.Errorf("events=%d players=%d, want 1/1", events, players)
	}
	// 旧版插件不带 eventId：不去重
	ev.EventID = ""
	_ = s.RecordRideEvent(ev)
	_ = s.RecordRideEvent(ev)
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ride_events`).Scan(&events)
	if events != 3 {
		t.Errorf("events=%d, want 3", events)
	}
}

// 营收事件按 eventId 幂等，分配写入 revenue_allocations。
func TestRevenueEventIdempotent(t *testing.T) {
	s := openTestStore(t)
	ev := model.RevenueEventData{
		EventID: "r1", Type: "ticket", Player: model.Player{UUID: "u1", Name: "Steve"},
		Amount: 3, Allocations: map[string]float64{"sysA": 2, "sysB": 1}, Time: time.Now().UnixMilli(),
	}
	for i := 0; i < 2; i++ {
		if err := s.RecordRevenueEvent(ev); err != nil {
			t.Fatalf("record #%d: %v", i, err)
		}
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM revenue_allocations`).Scan(&n)
	if n != 2 {
		t.Errorf("allocations=%d, want 2", n)
	}
	daily, err := s.SystemDailyRevenue("sysA", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 7 || daily[6].Value != 2 {
		t.Errorf("daily = %+v, want today=2", daily)
	}
	st, err := s.PlayerStats("u1")
	if err != nil {
		t.Fatal(err)
	}
	if st.TicketCount != 1 || st.TicketSpent != 3 {
		t.Errorf("ticket stats = %d/%v, want 1/3", st.TicketCount, st.TicketSpent)
	}
}

// 结算普通车乘车历史时记录经过的线路，并参与玩家统计。
func TestFinalizeRecordsLineIDsAndStats(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UnixMilli()
	p := []model.Player{{UUID: "u1", Name: "Steve"}}
	for i, st := range []struct{ node, name, line string }{
		{"n1", "A", "L1"}, {"n2", "", "L1"}, {"n3", "B", "L2"},
	} {
		if err := s.RecordRideEvent(model.RideEventData{
			TrainID: "t1", NodeID: st.node, StationName: st.name, LineID: st.line,
			Passengers: p, ArrivedAt: now + int64(i)*1000,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.FinalizeRide("t1"); err != nil {
		t.Fatal(err)
	}
	var lineIDs string
	if err := s.db.QueryRow(`SELECT line_ids FROM ride_history WHERE player_uuid = 'u1'`).Scan(&lineIDs); err != nil {
		t.Fatal(err)
	}
	if lineIDs != `["L1","L2"]` {
		t.Errorf("line_ids = %s", lineIDs)
	}
	st, err := s.PlayerStats("u1")
	if err != nil {
		t.Fatal(err)
	}
	if st.RideCount != 1 || st.VisitedStations != 2 || len(st.FavoriteLines) != 2 {
		t.Errorf("stats = %+v", st)
	}
	if st.DailyRides[len(st.DailyRides)-1].Value != 1 {
		t.Errorf("today rides = %v, want 1", st.DailyRides[len(st.DailyRides)-1].Value)
	}
	riders, err := s.LinesDailyRidership([]string{"L1", "L2"}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if riders[6].Value != 1 {
		t.Errorf("ridership today = %v, want 1 (同一玩家同一列车只计一次)", riders[6].Value)
	}
}

// 已有 ride_events 但 ride_event_players 为空时，迁移会回填。
func TestBackfillRideEventPlayers(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.db.Exec(`INSERT INTO ride_events (train_id, node_id, player_uuids) VALUES ('t', 'n', '["a","b"]')`); err != nil {
		t.Fatal(err)
	}
	if err := s.backfillRideEventPlayers(); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM ride_event_players`).Scan(&n)
	if n != 2 {
		t.Errorf("backfilled=%d, want 2", n)
	}
}

func TestAuditFilter(t *testing.T) {
	s := openTestStore(t)
	for _, a := range []string{"system.update", "system.delete", "settings.update"} {
		if err := s.InsertAudit(AuditEntry{ActorUUID: "u1", ActorName: "Steve", Action: a, Success: true}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListAudit(AuditFilter{Action: "system."}, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Errorf("total=%d, want 2", page.Total)
	}
	page, _ = s.ListAudit(AuditFilter{Actor: "Ste"}, 1, 2)
	if page.Total != 3 || len(page.Items) != 2 || page.TotalPages != 2 {
		t.Errorf("page = %+v", page)
	}
}

// 刷卡乘车事件的分配计入系统每日营收，但不计入购票次数。
func TestCardRideRevenue(t *testing.T) {
	s := openTestStore(t)
	now := time.Now().UnixMilli()
	if err := s.RecordRevenueEvent(model.RevenueEventData{
		EventID: "c1", Type: "card_ride", Player: model.Player{UUID: "u1"}, Amount: 4,
		Allocations: map[string]float64{"sysA": 4}, Time: now,
	}); err != nil {
		t.Fatal(err)
	}
	daily, err := s.SystemDailyRevenue("sysA", 3)
	if err != nil {
		t.Fatal(err)
	}
	if daily[2].Value != 4 {
		t.Errorf("today revenue = %v, want 4", daily[2].Value)
	}
	st, err := s.PlayerStats("u1")
	if err != nil {
		t.Fatal(err)
	}
	if st.TicketCount != 0 || st.CardRideCount != 1 || st.CardRideSpent != 4 {
		t.Errorf("stats = ticket %d, cardRide %d/%v", st.TicketCount, st.CardRideCount, st.CardRideSpent)
	}
}
