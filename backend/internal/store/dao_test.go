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

func TestExpressRideHistoryRequiresPaymentRouteCoverage(t *testing.T) {
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
		t.Fatalf("express history should not be written if a paid route node is uncovered; total=%d", history.Total)
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

// 玩家实际多经过了购票路线之外的节点（前后各一站），只要覆盖了全部购票节点仍应记历史。
// 起止时间取购票路线首/末节点的到达时刻，而非整段乘车的首尾。
func TestExpressRideHistoryAllowsExtraNodesBeyondRoute(t *testing.T) {
	s := openTestStore(t)
	player := model.Player{UUID: "uuid-2", Name: "Alex"}
	route := []string{"node-a", "switch-1", "node-b"}

	if err := s.RecordRidePayment(payment("train-extra", player, route)); err != nil {
		t.Fatalf("record payment: %v", err)
	}
	for _, ev := range []model.RideEventData{
		expressEvent("train-extra", "node-x", "X", 1000, player), // 购票路线之前
		expressEvent("train-extra", "node-a", "A", 2000, player),
		expressEvent("train-extra", "switch-1", "", 3000, player),
		expressEvent("train-extra", "node-b", "B", 4000, player),
		expressEvent("train-extra", "node-y", "Y", 5000, player), // 购票路线之后
	} {
		if err := s.RecordRideEvent(ev); err != nil {
			t.Fatalf("record extra-node event %s: %v", ev.NodeID, err)
		}
	}
	if err := s.FinalizeRide("train-extra"); err != nil {
		t.Fatalf("finalize extra ride: %v", err)
	}
	history, err := s.ListRideHistory(player.UUID, 1, 10)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if history.Total != 1 || len(history.Items) != 1 {
		t.Fatalf("expected one express history item despite extra nodes, total=%d len=%d", history.Total, len(history.Items))
	}
	item := history.Items[0]
	// 起止时间应落在购票路线首/末节点上，而非 node-x / node-y。
	if item.StartedAt != 2000 || item.EndedAt != 4000 {
		t.Fatalf("express interval should span paid route nodes only: startedAt=%d endedAt=%d", item.StartedAt, item.EndedAt)
	}
	if !reflect.DeepEqual(item.NodeIDs, route) {
		t.Fatalf("unexpected express route nodes: got %v want %v", item.NodeIDs, route)
	}
}

// 环线快速车：购票路线首尾同为 node-a（A→B→A 绕一圈）。玩家真正绕回 node-a 才算走完，
// 起止时间应跨越整圈（首个 node-a 到第二个 node-a），而非塌缩到同一时刻。
func TestExpressRideHistoryRecordsLoopBackToStart(t *testing.T) {
	s := openTestStore(t)
	player := model.Player{UUID: "uuid-3", Name: "Loop"}
	route := []string{"node-a", "switch-1", "node-b", "switch-2", "node-a"}

	if err := s.RecordRidePayment(payment("train-loop", player, route)); err != nil {
		t.Fatalf("record payment: %v", err)
	}
	for _, ev := range []model.RideEventData{
		expressEvent("train-loop", "node-a", "A", 1000, player),
		expressEvent("train-loop", "switch-1", "", 2000, player),
		expressEvent("train-loop", "node-b", "B", 3000, player),
		expressEvent("train-loop", "switch-2", "", 4000, player),
		expressEvent("train-loop", "node-a", "A", 5000, player), // 绕回起点
	} {
		if err := s.RecordRideEvent(ev); err != nil {
			t.Fatalf("record loop event %s: %v", ev.NodeID, err)
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
		t.Fatalf("expected one express loop history item, total=%d len=%d", history.Total, len(history.Items))
	}
	item := history.Items[0]
	if item.StartedAt != 1000 || item.EndedAt != 5000 {
		t.Fatalf("loop interval should span the full loop: startedAt=%d endedAt=%d", item.StartedAt, item.EndedAt)
	}
	if !reflect.DeepEqual(item.NodeIDs, route) {
		t.Fatalf("unexpected loop route nodes: got %v want %v", item.NodeIDs, route)
	}
}

// 环线快速车但玩家没绕回起点（坐到 node-b 就下车）。购票路线末节点 node-a 的第二次出现
// 无法匹配，不应记历史——这正是集合覆盖语义会误判、有序子序列语义能拦住的场景。
func TestExpressRideHistoryRejectsIncompleteLoop(t *testing.T) {
	s := openTestStore(t)
	player := model.Player{UUID: "uuid-4", Name: "Half"}
	route := []string{"node-a", "switch-1", "node-b", "switch-2", "node-a"}

	if err := s.RecordRidePayment(payment("train-half", player, route)); err != nil {
		t.Fatalf("record payment: %v", err)
	}
	for _, ev := range []model.RideEventData{
		expressEvent("train-half", "node-a", "A", 1000, player),
		expressEvent("train-half", "switch-1", "", 2000, player),
		expressEvent("train-half", "node-b", "B", 3000, player), // 在此下车，未绕回 node-a
	} {
		if err := s.RecordRideEvent(ev); err != nil {
			t.Fatalf("record half-loop event %s: %v", ev.NodeID, err)
		}
	}
	if err := s.FinalizeRide("train-half"); err != nil {
		t.Fatalf("finalize half loop: %v", err)
	}
	history, err := s.ListRideHistory(player.UUID, 1, 10)
	if err != nil {
		t.Fatalf("list history: %v", err)
	}
	if history.Total != 0 {
		t.Fatalf("incomplete loop should not be recorded; total=%d", history.Total)
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
