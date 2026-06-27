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

// RecordRideEvent stores one triggered platform/bcswitcher event and updates player ride history.
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
	res, err := s.db.Exec(
		`INSERT INTO ride_events
			(train_id, train_type, node_id, station_name, express, line_id, arrived_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ev.TrainID, ev.TrainType, ev.NodeID, ev.StationName, boolToInt(ev.Express), ev.LineID, ev.ArrivedAt,
	)
	if err != nil {
		return err
	}
	eventID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	current := make(map[string]model.Player, len(ev.Passengers))
	for _, p := range ev.Passengers {
		if p.UUID == "" {
			continue
		}
		current[p.UUID] = p
		if err := s.upsertRidePlayer(p, ev.ArrivedAt); err != nil {
			return err
		}
		query := `INSERT OR IGNORE INTO ride_event_players (train_id, event_id, player_uuid) VALUES (?, ?, ?)`
		if s.dialect == dialectMySQL {
			query = `INSERT IGNORE INTO ride_event_players (train_id, event_id, player_uuid) VALUES (?, ?, ?)`
		}
		if _, err := s.db.Exec(query, ev.TrainID, eventID, p.UUID); err != nil {
			return err
		}
	}

	existing, err := s.activeSessionPlayers(ev.TrainID)
	if err != nil {
		return err
	}
	for _, uuid := range existing {
		if _, ok := current[uuid]; !ok {
			if err := s.finishRideSession(uuid, ev.TrainID); err != nil {
				return err
			}
		}
	}
	for _, p := range ev.Passengers {
		if p.UUID == "" {
			continue
		}
		if ev.Express {
			if err := s.updateExpressSession(p, ev); err != nil {
				return err
			}
		} else if err := s.updateCommonSession(p, ev); err != nil {
			return err
		}
	}
	return nil
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

func (s *Store) activeSessionPlayers(trainID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT player_uuid FROM ride_sessions WHERE train_id = ?`, trainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var uuids []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, err
		}
		uuids = append(uuids, uuid)
	}
	return uuids, rows.Err()
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

type rideSession struct {
	PlayerUUID       string
	PlayerName       string
	TrainID          string
	TrainType        string
	Express          bool
	StartedAt        int64
	UpdatedAt        int64
	StartStation     string
	EndStation       string
	StationCount     int
	Distance         float64
	PaidFare         float64
	NodeIDs          []string
	CompletedNodeIDs []string
	RouteNodeIDs     []string
	AllPresent       bool
}

func (s *Store) loadRideSession(playerUUID, trainID string) (*rideSession, error) {
	row := s.db.QueryRow(
		`SELECT player_uuid, player_name, train_id, train_type, express, started_at, updated_at,
			start_station, end_station, station_count, distance, paid_fare,
			node_ids, completed_node_ids, route_node_ids, all_present
		 FROM ride_sessions WHERE player_uuid = ? AND train_id = ?`,
		playerUUID, trainID,
	)
	var sess rideSession
	var express, allPresent int
	var nodeJSON, completedJSON, routeJSON string
	if err := row.Scan(&sess.PlayerUUID, &sess.PlayerName, &sess.TrainID, &sess.TrainType, &express,
		&sess.StartedAt, &sess.UpdatedAt, &sess.StartStation, &sess.EndStation, &sess.StationCount,
		&sess.Distance, &sess.PaidFare, &nodeJSON, &completedJSON, &routeJSON, &allPresent); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	sess.Express = intToBool(express)
	sess.AllPresent = intToBool(allPresent)
	_ = json.Unmarshal([]byte(nodeJSON), &sess.NodeIDs)
	_ = json.Unmarshal([]byte(completedJSON), &sess.CompletedNodeIDs)
	_ = json.Unmarshal([]byte(routeJSON), &sess.RouteNodeIDs)
	return &sess, nil
}

func (s *Store) saveRideSession(sess rideSession) error {
	nodeJSON, _ := json.Marshal(sess.NodeIDs)
	completedJSON, _ := json.Marshal(sess.CompletedNodeIDs)
	routeJSON, _ := json.Marshal(sess.RouteNodeIDs)
	var query string
	if s.dialect == dialectMySQL {
		query = `INSERT INTO ride_sessions
			(player_uuid, player_name, train_id, train_type, express, started_at, updated_at,
			 start_station, end_station, station_count, distance, paid_fare,
			 node_ids, completed_node_ids, route_node_ids, all_present)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				player_name = VALUES(player_name),
				train_type = VALUES(train_type),
				express = VALUES(express),
				started_at = VALUES(started_at),
				updated_at = VALUES(updated_at),
				start_station = VALUES(start_station),
				end_station = VALUES(end_station),
				station_count = VALUES(station_count),
				distance = VALUES(distance),
				paid_fare = VALUES(paid_fare),
				node_ids = VALUES(node_ids),
				completed_node_ids = VALUES(completed_node_ids),
				route_node_ids = VALUES(route_node_ids),
				all_present = VALUES(all_present)`
	} else {
		query = `INSERT OR REPLACE INTO ride_sessions
			(player_uuid, player_name, train_id, train_type, express, started_at, updated_at,
			 start_station, end_station, station_count, distance, paid_fare,
			 node_ids, completed_node_ids, route_node_ids, all_present)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	}
	_, err := s.db.Exec(
		query,
		sess.PlayerUUID, sess.PlayerName, sess.TrainID, sess.TrainType, boolToInt(sess.Express),
		sess.StartedAt, sess.UpdatedAt, sess.StartStation, sess.EndStation, sess.StationCount,
		sess.Distance, sess.PaidFare, string(nodeJSON), string(completedJSON), string(routeJSON), boolToInt(sess.AllPresent),
	)
	return err
}

func (s *Store) updateCommonSession(player model.Player, ev model.RideEventData) error {
	sess, err := s.loadRideSession(player.UUID, ev.TrainID)
	if err != nil {
		return err
	}
	isPlatform := ev.StationName != ""
	if sess == nil {
		if !isPlatform {
			return nil
		}
		sess = &rideSession{
			PlayerUUID: player.UUID, PlayerName: player.Name, TrainID: ev.TrainID, TrainType: ev.TrainType,
			Express: false, StartedAt: ev.ArrivedAt, StartStation: ev.StationName, EndStation: ev.StationName,
			StationCount: 1, UpdatedAt: ev.ArrivedAt, AllPresent: true,
		}
		appendUniqueNode(&sess.NodeIDs, ev.NodeID)
		sess.CompletedNodeIDs = append([]string(nil), sess.NodeIDs...)
		return s.saveRideSession(*sess)
	}

	sess.PlayerName = player.Name
	sess.UpdatedAt = ev.ArrivedAt
	appendUniqueNode(&sess.NodeIDs, ev.NodeID)
	if isPlatform {
		if sess.StartStation == "" {
			sess.StartStation = ev.StationName
		}
		if sess.EndStation != ev.StationName {
			sess.StationCount++
		}
		sess.EndStation = ev.StationName
		sess.CompletedNodeIDs = append([]string(nil), sess.NodeIDs...)
	}
	if err := s.saveRideSession(*sess); err != nil {
		return err
	}
	if isPlatform && sess.StationCount >= 2 {
		return s.upsertRideHistory(*sess)
	}
	return nil
}

func (s *Store) updateExpressSession(player model.Player, ev model.RideEventData) error {
	pay, err := s.loadRidePayment(player.UUID, ev.TrainID)
	if err != nil || pay == nil {
		return err
	}
	if len(pay.RouteNodeIDs) == 0 {
		return nil
	}
	sess, err := s.loadRideSession(player.UUID, ev.TrainID)
	if err != nil {
		return err
	}
	if sess == nil {
		if ev.NodeID != pay.RouteNodeIDs[0] {
			return nil
		}
		sess = &rideSession{
			PlayerUUID: player.UUID, PlayerName: player.Name, TrainID: ev.TrainID, TrainType: pay.TrainType,
			Express: true, StartedAt: ev.ArrivedAt, StartStation: pay.StartStation, EndStation: pay.StartStation,
			UpdatedAt: ev.ArrivedAt, Distance: pay.Distance, PaidFare: pay.PaidFare, RouteNodeIDs: pay.RouteNodeIDs, AllPresent: true,
		}
	}
	sess.PlayerName = player.Name
	sess.UpdatedAt = ev.ArrivedAt
	sess.Distance = pay.Distance
	sess.PaidFare = pay.PaidFare
	sess.RouteNodeIDs = pay.RouteNodeIDs
	appendUniqueNode(&sess.NodeIDs, ev.NodeID)
	if err := s.saveRideSession(*sess); err != nil {
		return err
	}
	lastNode := pay.RouteNodeIDs[len(pay.RouteNodeIDs)-1]
	if ev.NodeID == lastNode && ev.StationName == pay.EndStation && sess.AllPresent {
		sess.StartStation = pay.StartStation
		sess.EndStation = pay.EndStation
		sess.NodeIDs = pay.RouteNodeIDs
		if err := s.upsertRideHistory(*sess); err != nil {
			return err
		}
		_, err := s.db.Exec(`DELETE FROM ride_sessions WHERE player_uuid = ? AND train_id = ?`, player.UUID, ev.TrainID)
		return err
	}
	return nil
}

func (s *Store) finishRideSession(playerUUID, trainID string) error {
	sess, err := s.loadRideSession(playerUUID, trainID)
	if err != nil || sess == nil {
		return err
	}
	if sess.Express {
		_, err = s.db.Exec(`DELETE FROM ride_sessions WHERE player_uuid = ? AND train_id = ?`, playerUUID, trainID)
		return err
	}
	if sess.StationCount >= 2 {
		if err := s.upsertRideHistory(*sess); err != nil {
			return err
		}
	}
	_, err = s.db.Exec(`DELETE FROM ride_sessions WHERE player_uuid = ? AND train_id = ?`, playerUUID, trainID)
	return err
}

func (s *Store) upsertRideHistory(sess rideSession) error {
	nodeIDs := sess.NodeIDs
	if !sess.Express {
		nodeIDs = sess.CompletedNodeIDs
	}
	if len(nodeIDs) < 2 || sess.StartStation == "" || sess.EndStation == "" || sess.StartStation == sess.EndStation {
		return nil
	}
	nodeJSON, _ := json.Marshal(nodeIDs)
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
		sess.PlayerUUID, sess.PlayerName, sess.TrainID, sess.TrainType, boolToInt(sess.Express),
		sess.StartedAt, sess.UpdatedAt, round2(sess.Distance), sess.StartStation, sess.EndStation,
		round2(sess.PaidFare), string(nodeJSON),
	)
	return err
}

func appendUniqueNode(nodes *[]string, nodeID string) bool {
	if nodeID == "" {
		return false
	}
	if len(*nodes) == 0 || (*nodes)[len(*nodes)-1] != nodeID {
		*nodes = append(*nodes, nodeID)
		return true
	}
	return false
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
