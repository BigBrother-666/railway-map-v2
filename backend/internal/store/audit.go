package store

import (
	"database/sql"
	"strings"
	"time"
)

// AuditEntry 是一条管理操作日志。
type AuditEntry struct {
	ID        int64  `json:"id"`
	ActorUUID string `json:"actorUuid"`
	ActorName string `json:"actorName"`
	Action    string `json:"action"`
	Target    string `json:"target"`
	Detail    string `json:"detail,omitempty"` // JSON 文本（变更前后差异等）
	Success   bool   `json:"success"`
	Reason    string `json:"reason,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// AuditFilter 是操作日志查询条件（零值字段不过滤）。
type AuditFilter struct {
	Actor  string // UUID 或玩家名（模糊）
	Action string // 前缀匹配，如 "system." 或 "system.update"
	From   int64  // 起始时间（毫秒，含）
	To     int64  // 结束时间（毫秒，含）
}

// AuditPage 是分页结果。
type AuditPage struct {
	Items      []AuditEntry `json:"items"`
	Page       int          `json:"page"`
	PageSize   int          `json:"pageSize"`
	Total      int          `json:"total"`
	TotalPages int          `json:"totalPages"`
}

// InsertAudit 写入一条操作日志。
func (s *Store) InsertAudit(e AuditEntry) error {
	if e.CreatedAt == 0 {
		e.CreatedAt = time.Now().UnixMilli()
	}
	_, err := s.db.Exec(
		`INSERT INTO admin_audit_log (actor_uuid, actor_name, action, target, detail, success, reason, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ActorUUID, e.ActorName, e.Action, e.Target, e.Detail, boolToInt(e.Success), e.Reason, e.CreatedAt)
	return err
}

// ListAudit 按条件分页查询操作日志（按时间倒序）。
func (s *Store) ListAudit(f AuditFilter, page, pageSize int) (AuditPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	var where []string
	var args []any
	if f.Actor != "" {
		where = append(where, "(actor_uuid = ? OR actor_name LIKE ?)")
		args = append(args, f.Actor, "%"+f.Actor+"%")
	}
	if f.Action != "" {
		where = append(where, "action LIKE ?")
		args = append(args, f.Action+"%")
	}
	if f.From > 0 {
		where = append(where, "created_at >= ?")
		args = append(args, f.From)
	}
	if f.To > 0 {
		where = append(where, "created_at <= ?")
		args = append(args, f.To)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM admin_audit_log`+cond, args...).Scan(&total); err != nil {
		return AuditPage{}, err
	}
	rows, err := s.db.Query(
		`SELECT id, actor_uuid, actor_name, action, target, detail, success, reason, created_at
		 FROM admin_audit_log`+cond+` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return AuditPage{}, err
	}
	defer rows.Close()
	items := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var actorName, target, detail, reason sql.NullString
		var success int
		if err := rows.Scan(&e.ID, &e.ActorUUID, &actorName, &e.Action, &target, &detail, &success, &reason, &e.CreatedAt); err != nil {
			return AuditPage{}, err
		}
		e.ActorName, e.Target, e.Detail, e.Reason = actorName.String, target.String, detail.String, reason.String
		e.Success = intToBool(success)
		items = append(items, e)
	}
	if err := rows.Err(); err != nil {
		return AuditPage{}, err
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	return AuditPage{Items: items, Page: page, PageSize: pageSize, Total: total, TotalPages: totalPages}, nil
}
