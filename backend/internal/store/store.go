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

	"railway-map-backend/internal/model"
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
	// geometry 在乘车历史结算时截取线路坐标快照（可为 nil，此时不存快照）
	geometry RouteGeometryProvider
}

// RouteGeometryProvider 按节点序列截取线路坐标快照（由 geo.Cache 实现，接口注入避免 store→geo 循环依赖）。
type RouteGeometryProvider interface {
	RouteGeometry(nodeIDs, segmentLineIDs, lineIDs []string) (geom *model.RouteGeometry, meters float64, complete bool)
}

// SetGeometryProvider 注入线路坐标快照提供者。
func (s *Store) SetGeometryProvider(p RouteGeometryProvider) {
	s.geometry = p
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
			geometry      MEDIUMTEXT,
			UNIQUE KEY uniq_ride_history (player_uuid, train_id, started_at),
			INDEX idx_ride_history_player_time (player_uuid, ended_at DESC)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			section    VARCHAR(32) PRIMARY KEY,
			payload    LONGTEXT,
			updated_by VARCHAR(64),
			updated_at BIGINT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS roles (
			id          BIGINT PRIMARY KEY AUTO_INCREMENT,
			name        VARCHAR(64) NOT NULL UNIQUE,
			description VARCHAR(255),
			builtin     TINYINT(1) NOT NULL DEFAULT 0,
			created_at  BIGINT
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS role_permissions (
			role_id BIGINT NOT NULL,
			perm    VARCHAR(64) NOT NULL,
			PRIMARY KEY (role_id, perm)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS user_roles (
			uuid       VARCHAR(64) NOT NULL,
			role_id    BIGINT NOT NULL,
			granted_by VARCHAR(64),
			granted_at BIGINT,
			PRIMARY KEY (uuid, role_id),
			INDEX idx_user_roles_role (role_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS admin_audit_log (
			id         BIGINT PRIMARY KEY AUTO_INCREMENT,
			actor_uuid VARCHAR(64),
			actor_name VARCHAR(128),
			action     VARCHAR(64),
			target     VARCHAR(255),
			detail     MEDIUMTEXT,
			success    TINYINT(1),
			reason     VARCHAR(255),
			created_at BIGINT,
			INDEX idx_audit_time (created_at),
			INDEX idx_audit_actor (actor_uuid)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS revenue_events (
			event_id    VARCHAR(64) PRIMARY KEY,
			type        VARCHAR(32),
			player_uuid VARCHAR(64),
			player_name VARCHAR(128),
			amount      DOUBLE,
			detail      VARCHAR(512),
			created_at  BIGINT,
			INDEX idx_revenue_player (player_uuid, created_at),
			INDEX idx_revenue_time (created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS revenue_allocations (
			event_id   VARCHAR(64) NOT NULL,
			system_id  VARCHAR(128) NOT NULL,
			amount     DOUBLE,
			created_at BIGINT,
			PRIMARY KEY (event_id, system_id),
			INDEX idx_alloc_system_time (system_id, created_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
		`CREATE TABLE IF NOT EXISTS ride_event_players (
			event_id    BIGINT NOT NULL,
			player_uuid VARCHAR(64) NOT NULL,
			PRIMARY KEY (event_id, player_uuid),
			INDEX idx_rep_player (player_uuid)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return s.migrateCommon("MEDIUMTEXT", "VARCHAR(64)", "TEXT")
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
			geometry      TEXT,
			UNIQUE (player_uuid, train_id, started_at)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_ride_history_player_time
			ON ride_history (player_uuid, ended_at DESC);`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			section    TEXT PRIMARY KEY,
			payload    TEXT,
			updated_by TEXT,
			updated_at INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS roles (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			name        TEXT NOT NULL UNIQUE,
			description TEXT,
			builtin     INTEGER NOT NULL DEFAULT 0,
			created_at  INTEGER
		);`,
		`CREATE TABLE IF NOT EXISTS role_permissions (
			role_id INTEGER NOT NULL,
			perm    TEXT NOT NULL,
			PRIMARY KEY (role_id, perm)
		);`,
		`CREATE TABLE IF NOT EXISTS user_roles (
			uuid       TEXT NOT NULL,
			role_id    INTEGER NOT NULL,
			granted_by TEXT,
			granted_at INTEGER,
			PRIMARY KEY (uuid, role_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_user_roles_role ON user_roles (role_id);`,
		`CREATE TABLE IF NOT EXISTS admin_audit_log (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			actor_uuid TEXT,
			actor_name TEXT,
			action     TEXT,
			target     TEXT,
			detail     TEXT,
			success    INTEGER,
			reason     TEXT,
			created_at INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_audit_time ON admin_audit_log (created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_audit_actor ON admin_audit_log (actor_uuid);`,
		`CREATE TABLE IF NOT EXISTS revenue_events (
			event_id    TEXT PRIMARY KEY,
			type        TEXT,
			player_uuid TEXT,
			player_name TEXT,
			amount      REAL,
			detail      TEXT,
			created_at  INTEGER
		);`,
		`CREATE INDEX IF NOT EXISTS idx_revenue_player ON revenue_events (player_uuid, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_revenue_time ON revenue_events (created_at);`,
		`CREATE TABLE IF NOT EXISTS revenue_allocations (
			event_id   TEXT NOT NULL,
			system_id  TEXT NOT NULL,
			amount     REAL,
			created_at INTEGER,
			PRIMARY KEY (event_id, system_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_alloc_system_time ON revenue_allocations (system_id, created_at);`,
		`CREATE TABLE IF NOT EXISTS ride_event_players (
			event_id    INTEGER NOT NULL,
			player_uuid TEXT NOT NULL,
			PRIMARY KEY (event_id, player_uuid)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_rep_player ON ride_event_players (player_uuid);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return s.migrateCommon("TEXT", "TEXT", "TEXT")
}

// migrateCommon 补加旧表的新列 / 索引，并一次性回填 ride_event_players。
//
// @param geomType   ride_history.geometry 列类型
// @param eventIDType ride_events.event_id 列类型
// @param lineIDsType ride_history.line_ids / segment_line_ids、ride_payments.route_line_ids 列类型
func (s *Store) migrateCommon(geomType, eventIDType, lineIDsType string) error {
	if err := s.ensureColumn("ride_history", "geometry", geomType); err != nil {
		return err
	}
	if err := s.ensureColumn("ride_history", "line_ids", lineIDsType); err != nil {
		return err
	}
	if err := s.ensureColumn("ride_history", "segment_line_ids", lineIDsType); err != nil {
		return err
	}
	if err := s.ensureColumn("ride_payments", "route_line_ids", lineIDsType); err != nil {
		return err
	}
	if err := s.ensureColumn("ride_events", "event_id", eventIDType); err != nil {
		return err
	}
	if err := s.ensureIndex("ride_events", "uniq_ride_events_event_id", "event_id", true); err != nil {
		return err
	}
	if err := s.ensureIndex("ride_events", "idx_ride_events_train", "train_id", false); err != nil {
		return err
	}
	return s.backfillRideEventPlayers()
}

// ensureColumn 为已存在的旧表补加列（CREATE TABLE IF NOT EXISTS 不会给旧表加新列）。
//
// @param table  表名
// @param column 列名
// @param typ    列类型
func (s *Store) ensureColumn(table, column, typ string) error {
	exists, err := s.columnExists(table, column)
	if err != nil || exists {
		return err
	}
	_, err = s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, typ))
	return err
}

// ensureIndex 为已存在的表补建索引（MySQL 不支持 CREATE INDEX IF NOT EXISTS，先查 information_schema）。
// 唯一索引允许多个 NULL，旧数据的 event_id 为 NULL 不冲突。
func (s *Store) ensureIndex(table, name, columns string, unique bool) error {
	kind := "INDEX"
	if unique {
		kind = "UNIQUE INDEX"
	}
	if s.dialect == dialectSQLite {
		_, err := s.db.Exec(fmt.Sprintf("CREATE %s IF NOT EXISTS %s ON %s (%s)", kind, name, table, columns))
		return err
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM information_schema.STATISTICS
		 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND INDEX_NAME = ?`, table, name,
	).Scan(&n); err != nil || n > 0 {
		return err
	}
	_, err := s.db.Exec(fmt.Sprintf("CREATE %s %s ON %s (%s)", kind, name, table, columns))
	return err
}

// columnExists 判断表中是否已有某列。
func (s *Store) columnExists(table, column string) (bool, error) {
	if s.dialect == dialectMySQL {
		var n int
		err := s.db.QueryRow(
			`SELECT COUNT(*) FROM information_schema.COLUMNS
			 WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`,
			table, column,
		).Scan(&n)
		return n > 0, err
	}
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
