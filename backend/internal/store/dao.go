package store

import (
	"database/sql"
	"encoding/json"
	"math"
	"time"

	"railway-map-backend/internal/model"
)

// Snapshot 是一份缓存快照（geo / lines / systems）。
type Snapshot struct {
	Version   string
	Payload   []byte
	UpdatedAt int64
}

// SaveSnapshot 落库一份快照（按 key 覆盖）。
func (s *Store) SaveSnapshot(key, version string, payload []byte) error {
	var query string
	if s.dialect == dialectMySQL {
		query = "INSERT INTO kv_snapshot (`key`, version, payload, updated_at) VALUES (?, ?, ?, ?) ON DUPLICATE KEY UPDATE version = VALUES(version), payload = VALUES(payload), updated_at = VALUES(updated_at)"
	} else {
		query = `INSERT OR REPLACE INTO kv_snapshot (key, version, payload, updated_at) VALUES (?, ?, ?, ?)`
	}
	_, err := s.db.Exec(query, key, version, payload, time.Now().UnixMilli())
	return err
}

// LoadSnapshot 读取一份快照；不存在返回 (nil, nil)。
func (s *Store) LoadSnapshot(key string) (*Snapshot, error) {
	keyColumn := "key"
	if s.dialect == dialectMySQL {
		keyColumn = "`key`"
	}
	row := s.db.QueryRow(`SELECT version, payload, updated_at FROM kv_snapshot WHERE `+keyColumn+` = ?`, key)
	var snap Snapshot
	if err := row.Scan(&snap.Version, &snap.Payload, &snap.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &snap, nil
}

// UpsertBoundPlayer 登记 / 更新一个允许登录网页的玩家。
func (s *Store) UpsertBoundPlayer(uuid, name string) error {
	var query string
	if s.dialect == dialectMySQL {
		query = `INSERT INTO bound_players (uuid, name, bound_at) VALUES (?, ?, ?)
			ON DUPLICATE KEY UPDATE name = VALUES(name), bound_at = VALUES(bound_at)`
	} else {
		query = `INSERT OR REPLACE INTO bound_players (uuid, name, bound_at) VALUES (?, ?, ?)`
	}
	_, err := s.db.Exec(query, uuid, name, time.Now().UnixMilli())
	return err
}

// DeleteBoundPlayer 移除一个绑定。
func (s *Store) DeleteBoundPlayer(uuid string) error {
	_, err := s.db.Exec(`DELETE FROM bound_players WHERE uuid = ?`, uuid)
	return err
}

// IsBoundPlayer 判断某玩家是否已绑定网页登录。
func (s *Store) IsBoundPlayer(uuid string) (bool, error) {
	row := s.db.QueryRow(`SELECT 1 FROM bound_players WHERE uuid = ?`, uuid)
	var one int
	if err := row.Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// LogPurchase 记录一次在线购票（成功与否都记，便于排障 / 统计）。
func (s *Store) LogPurchase(requestID, playerUUID, playerName, nodeIDs string, success bool, reason string, price float64) error {
	var query string
	if s.dialect == dialectMySQL {
		query = `INSERT INTO purchase_log
			(request_id, player_uuid, player_name, node_ids, success, reason, price, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				player_uuid = VALUES(player_uuid),
				player_name = VALUES(player_name),
				node_ids = VALUES(node_ids),
				success = VALUES(success),
				reason = VALUES(reason),
				price = VALUES(price),
				created_at = VALUES(created_at)`
	} else {
		query = `INSERT OR REPLACE INTO purchase_log
			(request_id, player_uuid, player_name, node_ids, success, reason, price, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	}
	_, err := s.db.Exec(
		query,
		requestID, playerUUID, playerName, nodeIDs, boolToInt(success), reason, price, time.Now().UnixMilli(),
	)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func intToBool(v int) bool {
	return v != 0
}

// RecordRidePayment stores the one-time pass payment metadata for an express ride.
func (s *Store) RecordRidePayment(pay model.RidePaymentData) error {
	if pay.PaidAt <= 0 {
		pay.PaidAt = time.Now().UnixMilli()
	}
	if pay.TrainType == "" {
		if pay.Express {
			pay.TrainType = "express"
		} else {
			pay.TrainType = "common"
		}
	}
	if err := s.upsertRidePlayer(pay.Player, pay.PaidAt); err != nil {
		return err
	}
	routeJSON, _ := json.Marshal(pay.RouteNodeIDs)
	var query string
	if s.dialect == dialectMySQL {
		query = `INSERT INTO ride_payments
			(train_id, player_uuid, player_name, train_type, express, start_station, end_station,
			 distance, paid_fare, route_node_ids, paid_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
			player_name = VALUES(player_name),
			train_type = VALUES(train_type),
			express = VALUES(express),
			start_station = VALUES(start_station),
			end_station = VALUES(end_station),
			distance = VALUES(distance),
			paid_fare = VALUES(paid_fare),
			route_node_ids = VALUES(route_node_ids),
			paid_at = VALUES(paid_at)`
	} else {
		query = `INSERT OR REPLACE INTO ride_payments
			(train_id, player_uuid, player_name, train_type, express, start_station, end_station,
			 distance, paid_fare, route_node_ids, paid_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	}
	_, err := s.db.Exec(
		query,
		pay.TrainID, pay.Player.UUID, pay.Player.Name, pay.TrainType, boolToInt(pay.Express),
		pay.StartStation, pay.EndStation, round2(pay.Distance), round2(pay.PaidFare), string(routeJSON), pay.PaidAt,
	)
	return err
}

// RecordRideEvent stores one triggered platform/bcswitcher event, recording the
// set of players aboard the train at that node. Ride history is built later from
// these events when the train is removed (see FinalizeRide).
func (s *Store) RecordRideEvent(ev model.RideEventData) error {
	if ev.ArrivedAt <= 0 {
		ev.ArrivedAt = time.Now().UnixMilli()
	}
	if ev.TrainType == "" {
		if ev.Express {
			ev.TrainType = "express"
		} else {
			ev.TrainType = "common"
		}
	}
	uuids := make([]string, 0, len(ev.Passengers))
	for _, p := range ev.Passengers {
		if p.UUID == "" {
			continue
		}
		uuids = append(uuids, p.UUID)
		if err := s.upsertRidePlayer(p, ev.ArrivedAt); err != nil {
			return err
		}
	}
	uuidsJSON, _ := json.Marshal(uuids)
	_, err := s.db.Exec(
		`INSERT INTO ride_events
			(train_id, train_type, node_id, station_name, express, line_id, arrived_at, player_uuids)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ev.TrainID, ev.TrainType, ev.NodeID, ev.StationName, boolToInt(ev.Express), ev.LineID, ev.ArrivedAt, string(uuidsJSON),
	)
	return err
}

func (s *Store) upsertRidePlayer(player model.Player, updatedAt int64) error {
	if player.UUID == "" {
		return nil
	}
	var query string
	if s.dialect == dialectMySQL {
		query = `INSERT INTO ride_players (uuid, name, updated_at) VALUES (?, ?, ?)
		 ON DUPLICATE KEY UPDATE name = VALUES(name), updated_at = VALUES(updated_at)`
	} else {
		query = `INSERT INTO ride_players (uuid, name, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(uuid) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`
	}
	_, err := s.db.Exec(query, player.UUID, player.Name, updatedAt)
	return err
}

// rideEventRow is one recorded ride event replayed during finalize.
type rideEventRow struct {
	nodeID      string
	stationName string
	trainType   string
	express     bool
	arrivedAt   int64
	players     []string
}

// FinalizeRide builds ride history for every player that appeared aboard the
// given train, from its recorded ride_events. Called when the train is removed
// (realtime.removed) or swept for timeout; idempotent via ride_history's unique key.
func (s *Store) FinalizeRide(trainID string) error {
	if trainID == "" {
		return nil
	}
	rows, err := s.db.Query(
		`SELECT node_id, station_name, train_type, express, arrived_at, player_uuids
		 FROM ride_events WHERE train_id = ? ORDER BY arrived_at ASC, id ASC`,
		trainID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	var events []rideEventRow
	present := map[string]struct{}{}
	for rows.Next() {
		var ev rideEventRow
		var express int
		var uuidsJSON string
		if err := rows.Scan(&ev.nodeID, &ev.stationName, &ev.trainType, &express, &ev.arrivedAt, &uuidsJSON); err != nil {
			return err
		}
		ev.express = intToBool(express)
		_ = json.Unmarshal([]byte(uuidsJSON), &ev.players)
		events = append(events, ev)
		for _, u := range ev.players {
			present[u] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for uuid := range present {
		if err := s.finalizeRidePlayer(uuid, trainID, events); err != nil {
			return err
		}
	}
	return nil
}

type ridePayment struct {
	TrainID      string
	PlayerUUID   string
	PlayerName   string
	TrainType    string
	Express      bool
	StartStation string
	EndStation   string
	Distance     float64
	PaidFare     float64
	RouteNodeIDs []string
	PaidAt       int64
}

func (s *Store) loadRidePayment(playerUUID, trainID string) (*ridePayment, error) {
	row := s.db.QueryRow(
		`SELECT train_id, player_uuid, player_name, train_type, express, start_station, end_station,
			distance, paid_fare, route_node_ids, paid_at
		 FROM ride_payments WHERE player_uuid = ? AND train_id = ?`,
		playerUUID, trainID,
	)
	var pay ridePayment
	var express int
	var routeJSON string
	if err := row.Scan(&pay.TrainID, &pay.PlayerUUID, &pay.PlayerName, &pay.TrainType, &express,
		&pay.StartStation, &pay.EndStation, &pay.Distance, &pay.PaidFare, &routeJSON, &pay.PaidAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	pay.Express = intToBool(express)
	_ = json.Unmarshal([]byte(routeJSON), &pay.RouteNodeIDs)
	return &pay, nil
}

// playerNodeStop is one node at which a player was present, in ride order.
type playerNodeStop struct {
	nodeID      string
	stationName string
	arrivedAt   int64
}

// finalizeRidePlayer builds and stores one player's ride history from the train's events.
func (s *Store) finalizeRidePlayer(playerUUID, trainID string, events []rideEventRow) error {
	var stops []playerNodeStop
	express := false
	trainType := ""
	for _, ev := range events {
		if !contains(ev.players, playerUUID) {
			continue
		}
		express = ev.express
		trainType = ev.trainType
		// 去重连续相同节点
		if len(stops) > 0 && stops[len(stops)-1].nodeID == ev.nodeID {
			continue
		}
		stops = append(stops, playerNodeStop{nodeID: ev.nodeID, stationName: ev.stationName, arrivedAt: ev.arrivedAt})
	}
	if len(stops) == 0 {
		return nil
	}
	name := s.ridePlayerName(playerUUID)
	if express {
		return s.finalizeExpress(playerUUID, name, trainID, trainType, stops)
	}
	return s.finalizeCommon(playerUUID, name, trainID, trainType, stops)
}

// finalizeExpress writes history only when the player's present-node sequence
// exactly equals the paid route (route_node_ids); a missing node writes nothing.
func (s *Store) finalizeExpress(playerUUID, playerName, trainID, trainType string, stops []playerNodeStop) error {
	pay, err := s.loadRidePayment(playerUUID, trainID)
	if err != nil || pay == nil || len(pay.RouteNodeIDs) == 0 {
		return err
	}
	nodeIDs := make([]string, len(stops))
	for i, st := range stops {
		nodeIDs[i] = st.nodeID
	}
	if !equalStrings(nodeIDs, pay.RouteNodeIDs) {
		return nil
	}
	if trainType == "" {
		trainType = pay.TrainType
	}
	return s.insertRideHistory(rideHistoryRow{
		playerUUID: playerUUID, playerName: playerName, trainID: trainID, trainType: trainType,
		express: true, startedAt: stops[0].arrivedAt, endedAt: stops[len(stops)-1].arrivedAt,
		distance: pay.Distance, paidFare: pay.PaidFare,
		startStation: pay.StartStation, endStation: pay.EndStation, nodeIDs: pay.RouteNodeIDs,
	})
}

// finalizeCommon trims the leading/trailing non-station nodes so the ride starts
// and ends on a station node, then writes the resulting interval to history.
func (s *Store) finalizeCommon(playerUUID, playerName, trainID, trainType string, stops []playerNodeStop) error {
	start, end := 0, len(stops)-1
	for start <= end && stops[start].stationName == "" {
		start++
	}
	for end >= start && stops[end].stationName == "" {
		end--
	}
	if start >= end {
		return nil
	}
	trimmed := stops[start : end+1]
	startStation := trimmed[0].stationName
	endStation := trimmed[len(trimmed)-1].stationName
	if startStation == "" || endStation == "" || startStation == endStation {
		return nil
	}
	nodeIDs := make([]string, len(trimmed))
	for i, st := range trimmed {
		nodeIDs[i] = st.nodeID
	}
	return s.insertRideHistory(rideHistoryRow{
		playerUUID: playerUUID, playerName: playerName, trainID: trainID, trainType: trainType,
		express: false, startedAt: trimmed[0].arrivedAt, endedAt: trimmed[len(trimmed)-1].arrivedAt,
		startStation: startStation, endStation: endStation, nodeIDs: nodeIDs,
	})
}

// rideHistoryRow is a finalized ride ready to be written to ride_history.
type rideHistoryRow struct {
	playerUUID   string
	playerName   string
	trainID      string
	trainType    string
	express      bool
	startedAt    int64
	endedAt      int64
	distance     float64
	paidFare     float64
	startStation string
	endStation   string
	nodeIDs      []string
}

func (s *Store) insertRideHistory(h rideHistoryRow) error {
	if len(h.nodeIDs) < 2 || h.startStation == "" || h.endStation == "" || h.startStation == h.endStation {
		return nil
	}
	nodeJSON, _ := json.Marshal(h.nodeIDs)
	var query string
	if s.dialect == dialectMySQL {
		query = `INSERT INTO ride_history
			(player_uuid, player_name, train_id, train_type, express, started_at, ended_at, distance,
			 start_station, end_station, paid_fare, node_ids)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
			player_name = VALUES(player_name),
			train_type = VALUES(train_type),
			express = VALUES(express),
			ended_at = VALUES(ended_at),
			distance = VALUES(distance),
			start_station = VALUES(start_station),
			end_station = VALUES(end_station),
			paid_fare = VALUES(paid_fare),
			node_ids = VALUES(node_ids)`
	} else {
		query = `INSERT INTO ride_history
			(player_uuid, player_name, train_id, train_type, express, started_at, ended_at, distance,
			 start_station, end_station, paid_fare, node_ids)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(player_uuid, train_id, started_at) DO UPDATE SET
			player_name = excluded.player_name,
			train_type = excluded.train_type,
			express = excluded.express,
			ended_at = excluded.ended_at,
			distance = excluded.distance,
			start_station = excluded.start_station,
			end_station = excluded.end_station,
			paid_fare = excluded.paid_fare,
			node_ids = excluded.node_ids`
	}
	_, err := s.db.Exec(
		query,
		h.playerUUID, h.playerName, h.trainID, h.trainType, boolToInt(h.express),
		h.startedAt, h.endedAt, round2(h.distance), h.startStation, h.endStation,
		round2(h.paidFare), string(nodeJSON),
	)
	return err
}

// ridePlayerName resolves a player's display name from ride_players; "" if unknown.
func (s *Store) ridePlayerName(uuid string) string {
	var name string
	if err := s.db.QueryRow(`SELECT name FROM ride_players WHERE uuid = ?`, uuid).Scan(&name); err != nil {
		return ""
	}
	return name
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func (s *Store) ListRideHistory(playerUUID string, page, pageSize int) (model.RideHistoryResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 50 {
		pageSize = 50
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ride_history WHERE player_uuid = ?`, playerUUID).Scan(&total); err != nil {
		return model.RideHistoryResponse{}, err
	}
	offset := (page - 1) * pageSize
	rows, err := s.db.Query(
		`SELECT id, train_id, train_type, express, started_at, ended_at, distance,
			start_station, end_station, paid_fare, node_ids
		 FROM ride_history WHERE player_uuid = ?
		 ORDER BY ended_at DESC, id DESC LIMIT ? OFFSET ?`,
		playerUUID, pageSize, offset,
	)
	if err != nil {
		return model.RideHistoryResponse{}, err
	}
	defer rows.Close()

	items := make([]model.RideHistoryItem, 0, pageSize)
	for rows.Next() {
		var item model.RideHistoryItem
		var express int
		var nodeJSON string
		if err := rows.Scan(&item.ID, &item.TrainID, &item.TrainType, &express, &item.StartedAt, &item.EndedAt,
			&item.Distance, &item.StartStation, &item.EndStation, &item.PaidFare, &nodeJSON); err != nil {
			return model.RideHistoryResponse{}, err
		}
		item.Express = intToBool(express)
		_ = json.Unmarshal([]byte(nodeJSON), &item.NodeIDs)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return model.RideHistoryResponse{}, err
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return model.RideHistoryResponse{
		Items: items, Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages,
	}, nil
}
