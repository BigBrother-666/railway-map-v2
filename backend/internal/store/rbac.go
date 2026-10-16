package store

import (
	"database/sql"
	"errors"
	"time"
)

// ErrNotFound 表示目标记录不存在。
var ErrNotFound = errors.New("not found")

// Role 是一个 RBAC 角色。
type Role struct {
	ID          int64    `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Builtin     bool     `json:"builtin"`
	CreatedAt   int64    `json:"createdAt"`
	Permissions []string `json:"permissions"`
	UserCount   int      `json:"userCount"`
}

// UserRoleRow 是一条用户授权记录（含玩家名，取自 bound_players / ride_players）。
type UserRoleRow struct {
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	RoleID    int64  `json:"roleId"`
	RoleName  string `json:"roleName"`
	GrantedBy string `json:"grantedBy"`
	GrantedAt int64  `json:"grantedAt"`
}

// PlayerRow 是一个已知玩家（用于授权 / 成员搜索）。
type PlayerRow struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// RoleByName 按名称查角色；不存在返回 ErrNotFound。
func (s *Store) RoleByName(name string) (*Role, error) {
	row := s.db.QueryRow(`SELECT id, name, description, builtin, created_at FROM roles WHERE name = ?`, name)
	return scanRole(row)
}

// RoleByID 按 ID 查角色（含权限）；不存在返回 ErrNotFound。
func (s *Store) RoleByID(id int64) (*Role, error) {
	row := s.db.QueryRow(`SELECT id, name, description, builtin, created_at FROM roles WHERE id = ?`, id)
	r, err := scanRole(row)
	if err != nil {
		return nil, err
	}
	r.Permissions, err = s.rolePermissions(id)
	return r, err
}

func scanRole(row *sql.Row) (*Role, error) {
	var r Role
	var desc sql.NullString
	var builtin int
	var createdAt sql.NullInt64
	if err := row.Scan(&r.ID, &r.Name, &desc, &builtin, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	r.Description = desc.String
	r.Builtin = intToBool(builtin)
	r.CreatedAt = createdAt.Int64
	return &r, nil
}

func (s *Store) rolePermissions(roleID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT perm FROM role_permissions WHERE role_id = ? ORDER BY perm`, roleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	perms := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

// ListRoles 列出全部角色（含权限与授权人数），内置角色在前。
func (s *Store) ListRoles() ([]Role, error) {
	rows, err := s.db.Query(
		`SELECT r.id, r.name, r.description, r.builtin, r.created_at,
			(SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = r.id)
		 FROM roles r ORDER BY r.builtin DESC, r.id ASC`)
	if err != nil {
		return nil, err
	}
	var roles []Role
	for rows.Next() {
		var r Role
		var desc sql.NullString
		var builtin int
		var createdAt sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Name, &desc, &builtin, &createdAt, &r.UserCount); err != nil {
			rows.Close()
			return nil, err
		}
		r.Description = desc.String
		r.Builtin = intToBool(builtin)
		r.CreatedAt = createdAt.Int64
		roles = append(roles, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range roles {
		if roles[i].Permissions, err = s.rolePermissions(roles[i].ID); err != nil {
			return nil, err
		}
	}
	return roles, nil
}

// CreateRole 新建角色并写入权限，返回新角色 ID。
func (s *Store) CreateRole(name, description string, builtin bool, perms []string) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO roles (name, description, builtin, created_at) VALUES (?, ?, ?, ?)`,
		name, description, boolToInt(builtin), time.Now().UnixMilli())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := replacePermsTx(tx, id, perms); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// UpdateRole 修改角色名称、描述与权限（整体替换权限）。
func (s *Store) UpdateRole(id int64, name, description string, perms []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE roles SET name = ?, description = ? WHERE id = ?`, name, description, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// MySQL 在值未变化时 RowsAffected 也为 0，需再确认是否存在
		var one int
		if err := tx.QueryRow(`SELECT 1 FROM roles WHERE id = ?`, id).Scan(&one); err != nil {
			if err == sql.ErrNoRows {
				return ErrNotFound
			}
			return err
		}
	}
	if err := replacePermsTx(tx, id, perms); err != nil {
		return err
	}
	return tx.Commit()
}

// SetRolePermissions 整体替换某角色的权限（用于启动时同步内置角色）。
func (s *Store) SetRolePermissions(id int64, perms []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replacePermsTx(tx, id, perms); err != nil {
		return err
	}
	return tx.Commit()
}

func replacePermsTx(tx *sql.Tx, roleID int64, perms []string) error {
	if _, err := tx.Exec(`DELETE FROM role_permissions WHERE role_id = ?`, roleID); err != nil {
		return err
	}
	for _, p := range perms {
		if _, err := tx.Exec(`INSERT INTO role_permissions (role_id, perm) VALUES (?, ?)`, roleID, p); err != nil {
			return err
		}
	}
	return nil
}

// DeleteRole 删除角色，级联删除其权限与授权。
func (s *Store) DeleteRole(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM user_roles WHERE role_id = ?`,
		`DELETE FROM role_permissions WHERE role_id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	res, err := tx.Exec(`DELETE FROM roles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// GrantRole 给玩家授予角色（已授予则忽略）。
func (s *Store) GrantRole(uuid string, roleID int64, grantedBy string) error {
	var query string
	if s.dialect == dialectMySQL {
		query = `INSERT IGNORE INTO user_roles (uuid, role_id, granted_by, granted_at) VALUES (?, ?, ?, ?)`
	} else {
		query = `INSERT OR IGNORE INTO user_roles (uuid, role_id, granted_by, granted_at) VALUES (?, ?, ?, ?)`
	}
	_, err := s.db.Exec(query, uuid, roleID, grantedBy, time.Now().UnixMilli())
	return err
}

// RevokeRole 撤销玩家的某个角色。
func (s *Store) RevokeRole(uuid string, roleID int64) error {
	_, err := s.db.Exec(`DELETE FROM user_roles WHERE uuid = ? AND role_id = ?`, uuid, roleID)
	return err
}

// HasRole 判断玩家是否拥有某角色。
func (s *Store) HasRole(uuid string, roleID int64) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM user_roles WHERE uuid = ? AND role_id = ?`, uuid, roleID).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// PermissionsOf 返回玩家经由全部角色获得的权限（去重）。
func (s *Store) PermissionsOf(uuid string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT rp.perm FROM user_roles ur
		 JOIN role_permissions rp ON rp.role_id = ur.role_id
		 WHERE ur.uuid = ? ORDER BY rp.perm`, uuid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	perms := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		perms = append(perms, p)
	}
	return perms, rows.Err()
}

// ListUserRoles 列出全部用户授权（按玩家分组排序）。
func (s *Store) ListUserRoles() ([]UserRoleRow, error) {
	rows, err := s.db.Query(
		`SELECT ur.uuid, COALESCE(bp.name, rp.name, ''), ur.role_id, r.name,
			COALESCE(ur.granted_by, ''), COALESCE(ur.granted_at, 0)
		 FROM user_roles ur
		 JOIN roles r ON r.id = ur.role_id
		 LEFT JOIN bound_players bp ON bp.uuid = ur.uuid
		 LEFT JOIN ride_players rp ON rp.uuid = ur.uuid
		 ORDER BY ur.uuid, r.builtin DESC, r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserRoleRow{}
	for rows.Next() {
		var u UserRoleRow
		if err := rows.Scan(&u.UUID, &u.Name, &u.RoleID, &u.RoleName, &u.GrantedBy, &u.GrantedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SearchPlayers 按名称或 UUID 前缀搜索已知玩家（已绑定网页登录或乘过车），最多 limit 条。
func (s *Store) SearchPlayers(q string, limit int) ([]PlayerRow, error) {
	like := q + "%"
	nameLike := "%" + q + "%"
	rows, err := s.db.Query(
		`SELECT uuid, name FROM (
			SELECT uuid, name FROM bound_players
			UNION
			SELECT uuid, name FROM ride_players WHERE uuid NOT IN (SELECT uuid FROM bound_players)
		 ) p WHERE uuid LIKE ? OR name LIKE ? ORDER BY name LIMIT ?`,
		like, nameLike, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlayerRow{}
	for rows.Next() {
		var p PlayerRow
		var name sql.NullString
		if err := rows.Scan(&p.UUID, &name); err != nil {
			return nil, err
		}
		p.Name = name.String
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlayerNames 批量解析玩家名（bound_players 优先，其次 ride_players）；未知的 UUID 不出现在结果中。
func (s *Store) PlayerNames(uuids []string) (map[string]string, error) {
	out := make(map[string]string, len(uuids))
	for _, u := range uuids {
		var name string
		err := s.db.QueryRow(
			`SELECT name FROM bound_players WHERE uuid = ?
			 UNION ALL SELECT name FROM ride_players WHERE uuid = ? LIMIT 1`, u, u).Scan(&name)
		if err == nil && name != "" {
			out[u] = name
		} else if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
	}
	return out, nil
}
