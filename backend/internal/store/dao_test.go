package store

import (
	"path/filepath"
	"reflect"
	"testing"

	"railway-map-backend/internal/model"
)

func TestRidePlayersMappingIsGlobal(t *testing.T) {
	s := openTestStore(t)
	player := model.Player{UUID: "uuid-1", Name: "Steve"}
	if err := s.RecordRideEvent(model.RideEventData{
		TrainID:     "train-1",
		TrainType:   "common",
		NodeID:      "node-a",
		StationName: "A",
		Passengers:  []model.Player{player},
		ArrivedAt:   1000,
	}); err != nil {
		t.Fatalf("record ride event: %v", err)
	}

	rows, err := s.db.Query(`PRAGMA table_info(ride_players)`)
	if err != nil {
		t.Fatalf("pragma ride_players: %v", err)
	}
	defer rows.Close()

	columns := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column: %v", err)
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("column rows: %v", err)
	}
	for _, name := range []string{"uuid", "name", "updated_at"} {
		if !columns[name] {
			t.Fatalf("ride_players missing column %q; columns=%v", name, columns)
		}
	}
	if columns["train_id"] {
		t.Fatalf("ride_players must be global and must not contain train_id")
	}
}

func TestCommonRideHistoryUsesCompletedPlatformInterval(t *testing.T) {
	s := openTestStore(t)
	player := model.Player{UUID: "uuid-1", Name: "Steve"}
	events := []model.RideEventData{
		commonEvent("train-common", "node-a", "A", 1000, player),
		commonEvent("train-common", "switch-1", "", 2000, player),
		commonEvent("train-common", "node-b", "B", 3000, player),
		commonEvent("train-common", "switch-2", "", 4000, player),
		commonEvent("train-common", "node-c", "C", 5000, player),
		commonEvent("train-common", "switch-3", "", 6000),
	}
	for _, ev := range events {
		if err := s.RecordRideEvent(ev); err != nil {
			t.Fatalf("record ride event %s: %v", ev.NodeID, err)
		}
	}
	if err := s.FinalizeRide("train-common"); err != nil {
		t.Fatalf("finalize ride: %v", err)
	}

	history, err := s.ListRideHistory(player.UUID, 1, 10)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if history.Total != 1 || len(history.Items) != 1 {
		t.Fatalf("expected one history item, total=%d len=%d", history.Total, len(history.Items))
	}
	item := history.Items[0]
	if item.StartStation != "A" || item.EndStation != "C" {
		t.Fatalf("expected completed A -> C, got %s -> %s", item.StartStation, item.EndStation)
	}
	if item.EndedAt != 5000 {
		t.Fatalf("expected history to end at last completed platform, got %d", item.EndedAt)
	}
	wantNodes := []string{"node-a", "switch-1", "node-b", "switch-2", "node-c"}
	if !reflect.DeepEqual(item.NodeIDs, wantNodes) {
		t.Fatalf("unexpected completed nodes: got %v want %v", item.NodeIDs, wantNodes)
	}
}

func TestExpressRideHistoryRequiresPaymentRouteAndFullPresence(t *testing.T) {
	s := openTestStore(t)
	player := model.Player{UUID: "uuid-1", Name: "Steve"}
	route := []string{"node-a", "switch-1", "node-b"}

	if err := s.RecordRidePayment(payment("train-miss", player, route)); err != nil {
		t.Fatalf("record payment: %v", err)
	}
	for _, ev := range []model.RideEventData{
		expressEvent("train-miss", "node-a", "A", 1000, player),
		expressEvent("train-miss", "switch-1", "", 2000),
		expressEvent("train-miss", "node-b", "B", 3000, player),
	} {
		if err := s.RecordRideEvent(ev); err != nil {
			t.Fatalf("record missing-passenger event %s: %v", ev.NodeID, err)
		}
	}
	if err := s.FinalizeRide("train-miss"); err != nil {
		t.Fatalf("finalize missing ride: %v", err)
	}
	history, err := s.ListRideHistory(player.UUID, 1, 10)
	if err != nil {
		t.Fatalf("list history after missing passenger: %v", err)
	}
	if history.Total != 0 {
		t.Fatalf("express history should not be written if passenger missed an event; total=%d", history.Total)
	}

	if err := s.RecordRidePayment(payment("train-full", player, route)); err != nil {
		t.Fatalf("record payment full: %v", err)
	}
	for _, ev := range []model.RideEventData{
		expressEvent("train-full", "node-a", "A", 4000, player),
		expressEvent("train-full", "switch-1", "", 5000, player),
		expressEvent("train-full", "node-b", "B", 6000, player),
	} {
		if err := s.RecordRideEvent(ev); err != nil {
			t.Fatalf("record full event %s: %v", ev.NodeID, err)
		}
	}
	if err := s.FinalizeRide("train-full"); err != nil {
		t.Fatalf("finalize full ride: %v", err)
	}
	history, err = s.ListRideHistory(player.UUID, 1, 10)
	if err != nil {
		t.Fatalf("list history after full ride: %v", err)
	}
	if history.Total != 1 || len(history.Items) != 1 {
		t.Fatalf("expected one express history item, total=%d len=%d", history.Total, len(history.Items))
	}
	item := history.Items[0]
	if !item.Express || item.StartStation != "A" || item.EndStation != "B" {
		t.Fatalf("unexpected express history item: %+v", item)
	}
	if item.Distance != 1.23 || item.PaidFare != 0 {
		t.Fatalf("unexpected express distance/fare: distance=%v paid=%v", item.Distance, item.PaidFare)
	}
	if !reflect.DeepEqual(item.NodeIDs, route) {
		t.Fatalf("unexpected express route nodes: got %v want %v", item.NodeIDs, route)
	}
}

func TestCommonRideHistoryRecordsLoopRoute(t *testing.T) {
	s := openTestStore(t)
	player := model.Player{UUID: "uuid-1", Name: "Steve"}
	// B -> C -> A -> B 折返：首尾同名站，仍应记录一条历史。
	events := []model.RideEventData{
		commonEvent("train-loop", "node-b", "B", 1000, player),
		commonEvent("train-loop", "switch-1", "", 2000, player),
		commonEvent("train-loop", "node-c", "C", 3000, player),
		commonEvent("train-loop", "switch-2", "", 4000, player),
		commonEvent("train-loop", "node-a", "A", 5000, player),
		commonEvent("train-loop", "switch-3", "", 6000, player),
		commonEvent("train-loop", "node-b", "B", 7000, player),
	}
	for _, ev := range events {
		if err := s.RecordRideEvent(ev); err != nil {
			t.Fatalf("record ride event %s: %v", ev.NodeID, err)
		}
	}
	if err := s.FinalizeRide("train-loop"); err != nil {
		t.Fatalf("finalize loop ride: %v", err)
	}
	history, err := s.ListRideHistory(player.UUID, 1, 10)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if history.Total != 1 || len(history.Items) != 1 {
		t.Fatalf("expected one loop history item, total=%d len=%d", history.Total, len(history.Items))
	}
	item := history.Items[0]
	if item.StartStation != "B" || item.EndStation != "B" {
		t.Fatalf("expected loop B -> B, got %s -> %s", item.StartStation, item.EndStation)
	}
	wantNodes := []string{"node-b", "switch-1", "node-c", "switch-2", "node-a", "switch-3", "node-b"}
	if !reflect.DeepEqual(item.NodeIDs, wantNodes) {
		t.Fatalf("unexpected loop nodes: got %v want %v", item.NodeIDs, wantNodes)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := openSQLite(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	})
	return s
}

func commonEvent(trainID, nodeID, stationName string, arrivedAt int64, passengers ...model.Player) model.RideEventData {
	return model.RideEventData{
		TrainID:     trainID,
		TrainType:   "common",
		NodeID:      nodeID,
		StationName: stationName,
		Passengers:  passengers,
		ArrivedAt:   arrivedAt,
		Express:     false,
	}
}

func expressEvent(trainID, nodeID, stationName string, arrivedAt int64, passengers ...model.Player) model.RideEventData {
	return model.RideEventData{
		TrainID:     trainID,
		TrainType:   "express",
		NodeID:      nodeID,
		StationName: stationName,
		Passengers:  passengers,
		ArrivedAt:   arrivedAt,
		Express:     true,
	}
}

func payment(trainID string, player model.Player, route []string) model.RidePaymentData {
	return model.RidePaymentData{
		TrainID:      trainID,
		TrainType:    "express",
		Player:       player,
		Express:      true,
		RouteNodeIDs: route,
		StartStation: "A",
		EndStation:   "B",
		Distance:     1.23,
		PaidFare:     0,
		PaidAt:       900,
	}
}
