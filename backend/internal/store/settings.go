package store

import (
	"database/sql"
	"time"
)

// Setting 是一段持久化的服务器配置（app_settings 一行，payload 为 JSON）。
type Setting struct {
	Section   string
	Payload   []byte
	UpdatedBy string
	UpdatedAt int64
}

// LoadSetting 读取一段配置；不存在返回 (nil, nil)。
func (s *Store) LoadSetting(section string) (*Setting, error) {
	row := s.db.QueryRow(`SELECT section, payload, updated_by, updated_at FROM app_settings WHERE section = ?`, section)
	var st Setting
	var payload string
	if err := row.Scan(&st.Section, &payload, &st.UpdatedBy, &st.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	st.Payload = []byte(payload)
	return &st, nil
}

// SaveSetting 整段覆盖保存一段配置。
func (s *Store) SaveSetting(section string, payload []byte, updatedBy string) error {
	var query string
	if s.dialect == dialectMySQL {
		query = `INSERT INTO app_settings (section, payload, updated_by, updated_at) VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE payload = VALUES(payload), updated_by = VALUES(updated_by), updated_at = VALUES(updated_at)`
	} else {
		query = `INSERT OR REPLACE INTO app_settings (section, payload, updated_by, updated_at) VALUES (?, ?, ?, ?)`
	}
	_, err := s.db.Exec(query, section, string(payload), updatedBy, time.Now().UnixMilli())
	return err
}

// DeleteSetting 删除一段配置（恢复默认值）。
func (s *Store) DeleteSetting(section string) error {
	_, err := s.db.Exec(`DELETE FROM app_settings WHERE section = ?`, section)
	return err
}
