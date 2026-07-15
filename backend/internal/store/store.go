// Package store provides database persistence for backend state and history.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "modernc.org/sqlite"
)

type dialect string

const (
	dialectMySQL  dialect = "mysql"
	dialectSQLite dialect = "sqlite"
)

// Store holds the database connection and exposes table access methods.
type Store struct {
	db      *sql.DB
	dialect dialect
}

// Open connects to the configured database and runs migrations.
func Open(driver, dsn, path string) (*Store, error) {
	switch dialect(strings.ToLower(strings.TrimSpace(driver))) {
	case "", dialectMySQL:
		return openMySQL(dsn)
	case dialectSQLite:
		return openSQLite(path)
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func openMySQL(dsn string) (*Store, error) {
	db, err := sql.Open(string(dialectMySQL), dsn)
	if err != nil {
		return nil, err
	}
	db.SetConnMaxLifetime(3 * time.Minute)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, dialect: dialectMySQL}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func openSQLite(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open(string(dialectSQLite), path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db, dialect: dialectSQLite}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	if s.dialect == dialectMySQL {
		return s.migrateMySQL()
	}
	return s.migrateSQLite()
}

func (s *Store) migrateMySQL() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS kv_snapshot (
			` + "`key`" + ` VARCHAR(64) PRIMARY KEY,
			version    VARCHAR(128),
			payload    LONGBLOB,
			updated_at BIGINT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS bound_players (
			uuid       VARCHAR(64) PRIMARY KEY,
			name       VARCHAR(128),
			bound_at   BIGINT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS purchase_log (
			request_id   VARCHAR(128) PRIMARY KEY,
			player_uuid  VARCHAR(64),
			player_name  VARCHAR(128),
			node_ids     TEXT,
			success      TINYINT(1),
			reason       VARCHAR(128),
			price        DOUBLE,
			created_at   BIGINT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS ride_events (
			id            BIGINT PRIMARY KEY AUTO_INCREMENT,
			train_id      VARCHAR(128),
			train_type    VARCHAR(32),
			node_id       VARCHAR(128),
			station_name  VARCHAR(255),
			express       TINYINT(1),
			line_id       VARCHAR(128),
			arrived_at    BIGINT,
			player_uuids  TEXT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS ride_players (
			uuid          VARCHAR(64) PRIMARY KEY,
			name          VARCHAR(128),
			updated_at    BIGINT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS ride_payments (
			train_id       VARCHAR(128),
			player_uuid    VARCHAR(64),
			player_name    VARCHAR(128),
			train_type     VARCHAR(32),
			express        TINYINT(1),
			start_station  VARCHAR(255),
			end_station    VARCHAR(255),
			distance       DOUBLE,
			paid_fare      DOUBLE,
			route_node_ids TEXT,
			paid_at        BIGINT,
			PRIMARY KEY (train_id, player_uuid)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS ride_sessions (
			player_uuid        VARCHAR(64),
			player_name        VARCHAR(128),
			train_id           VARCHAR(128),
			train_type         VARCHAR(32),
			express            TINYINT(1),
			started_at         BIGINT,
			updated_at         BIGINT,
			start_station      VARCHAR(255),
			end_station        VARCHAR(255),
			station_count      INT,
			distance           DOUBLE,
			paid_fare          DOUBLE,
			node_ids           TEXT,
			completed_node_ids TEXT,
			route_node_ids     TEXT,
			all_present        TINYINT(1),
			PRIMARY KEY (player_uuid, train_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS ride_history (
			id            BIGINT PRIMARY KEY AUTO_INCREMENT,
			player_uuid   VARCHAR(64),
			player_name   VARCHAR(128),
			train_id      VARCHAR(128),
			train_type    VARCHAR(32),
			express       TINYINT(1),
			started_at    BIGINT,
			ended_at      BIGINT,
			distance      DOUBLE,
			start_station VARCHAR(255),
			end_station   VARCHAR(255),
			paid_fare     DOUBLE,
			node_ids      TEXT,
			UNIQUE KEY uniq_ride_history (player_uuid, train_id, started_at),
			INDEX idx_ride_history_player_time (player_uuid, ended_at DESC)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrateSQLite() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS kv_snapshot (
			key        TEXT PRIMARY KEY,
			version    TEXT,
			payload    BLOB,
			updated_at INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS bound_players (
			uuid       TEXT PRIMARY KEY,
			name       TEXT,
			bound_at   INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS purchase_log (
			request_id   TEXT PRIMARY KEY,
			player_uuid  TEXT,
			player_name  TEXT,
			node_ids     TEXT,
			success      INTEGER,
			reason       TEXT,
			price        REAL,
			created_at   INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS ride_events (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			train_id      TEXT,
			train_type    TEXT,
			node_id       TEXT,
			station_name  TEXT,
			express       INTEGER,
			line_id       TEXT,
			arrived_at    INTEGER,
			player_uuids  TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS ride_players (
			uuid          TEXT PRIMARY KEY,
			name          TEXT,
			updated_at    INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS ride_payments (
			train_id      TEXT,
			player_uuid   TEXT,
			player_name   TEXT,
			train_type    TEXT,
			express       INTEGER,
			start_station TEXT,
			end_station   TEXT,
			distance      REAL,
			paid_fare     REAL,
			route_node_ids TEXT,
			paid_at       INTEGER,
			PRIMARY KEY (train_id, player_uuid)
		);`,
		`CREATE TABLE IF NOT EXISTS ride_sessions (
			player_uuid   TEXT,
			player_name   TEXT,
			train_id      TEXT,
			train_type    TEXT,
			express       INTEGER,
			started_at    INTEGER,
			updated_at    INTEGER,
			start_station TEXT,
			end_station   TEXT,
			station_count INTEGER,
			distance      REAL,
			paid_fare     REAL,
			node_ids      TEXT,
			completed_node_ids TEXT,
			route_node_ids TEXT,
			all_present   INTEGER,
			PRIMARY KEY (player_uuid, train_id)
		);`,
		`CREATE TABLE IF NOT EXISTS ride_history (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			player_uuid   TEXT,
			player_name   TEXT,
			train_id      TEXT,
			train_type    TEXT,
			express       INTEGER,
			started_at    INTEGER,
			ended_at      INTEGER,
			distance      REAL,
			start_station TEXT,
			end_station   TEXT,
			paid_fare     REAL,
			node_ids      TEXT,
			UNIQUE (player_uuid, train_id, started_at)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_ride_history_player_time
			ON ride_history (player_uuid, ended_at DESC);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
