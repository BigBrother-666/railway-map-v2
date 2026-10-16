package store

import (
	"database/sql"
	"encoding/json"
	"time"

	"railway-map-backend/internal/model"
)

// backfillRideEventPlayers 一次性把 ride_events.player_uuids（JSON 文本）展开到 ride_event_players，
// 供按玩家统计时走索引。只在 ride_event_players 为空而 ride_events 有数据时执行。
func (s *Store) backfillRideEventPlayers() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ride_event_players`).Scan(&n); err != nil || n > 0 {
		return err
	}
	rows, err := s.db.Query(`SELECT id, player_uuids FROM ride_events`)
	if err != nil {
		return err
	}
	type pair struct {
		id    int64
		uuids []string
	}
	var all []pair
	for rows.Next() {
		var p pair
		var raw sql.NullString
		if err := rows.Scan(&p.id, &raw); err != nil {
			rows.Close()
			return err
		}
		_ = json.Unmarshal([]byte(raw.String), &p.uuids)
		if len(p.uuids) > 0 {
			all = append(all, p)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(all) == 0 {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(s.insertIgnore("ride_event_players", "event_id, player_uuid", "?, ?"))
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, p := range all {
		for _, u := range p.uuids {
			if _, err := stmt.Exec(p.id, u); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// insertIgnore 拼接「主键冲突则忽略」的 INSERT 语句（两种方言写法不同）。
func (s *Store) insertIgnore(table, columns, placeholders string) string {
	verb := "INSERT OR IGNORE"
	if s.dialect == dialectMySQL {
		verb = "INSERT IGNORE"
	}
	return verb + " INTO " + table + " (" + columns + ") VALUES (" + placeholders + ")"
}

// rideEventExists 判断带 eventId 的乘车事件是否已落库（离线补发去重）。
func (s *Store) rideEventExists(eventID string) (bool, error) {
	if eventID == "" {
		return false, nil
	}
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM ride_events WHERE event_id = ?`, eventID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// RecordRevenueEvent 落库一条营收事件及其按系统分配；eventId 重复时忽略（幂等）。
func (s *Store) RecordRevenueEvent(ev model.RevenueEventData) error {
	if ev.Time <= 0 {
		ev.Time = time.Now().UnixMilli()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(
		s.insertIgnore("revenue_events", "event_id, type, player_uuid, player_name, amount, detail, created_at", "?, ?, ?, ?, ?, ?, ?"),
		ev.EventID, ev.Type, ev.Player.UUID, ev.Player.Name, round2(ev.Amount), ev.Detail, ev.Time)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // 已落库过（补发重复）
	}
	for systemID, amount := range ev.Allocations {
		if systemID == "" {
			continue
		}
		if _, err := tx.Exec(
			s.insertIgnore("revenue_allocations", "event_id, system_id, amount, created_at", "?, ?, ?, ?"),
			ev.EventID, systemID, amount, ev.Time); err != nil {
			return err
		}
	}
	return tx.Commit()
}
