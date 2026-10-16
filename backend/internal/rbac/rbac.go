// Package rbac 实现个人中心的角色权限：权限点是代码常量，角色与授权存数据库。
// 内置角色 super_admin 拥有全部权限，不可删除、不可修改权限；环境变量指定的超管 UUID 启动时自动授予且不可撤销。
package rbac

import (
	"errors"
	"log/slog"
	"slices"
	"strings"

	"railway-map-backend/internal/store"
)

// 权限点。
const (
	PermRBAC         = "admin.rbac"        // 管理角色与用户授权
	PermSettings     = "admin.settings"    // 查看 / 修改服务器配置
	PermAudit        = "admin.audit"       // 查看操作日志
	PermSystemManage = "system.manage_all" // 编辑 / 删除任意铁路系统与线路
	PermStationAlias = "station.alias"     // 增删改车站别名
	PermStationGroup = "station.group"     // 增删改车站组
)

// PermissionInfo 描述一个权限点（供前端渲染勾选框）。
type PermissionInfo struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// AllPermissions 是全部权限点，顺序即前端展示顺序。
var AllPermissions = []PermissionInfo{
	{PermSystemManage, "管理全部铁路系统", "编辑 / 删除任意铁路系统及其线路、Logo，侧栏显示全部系统"},
	{PermStationAlias, "车站别名", "新增 / 修改 / 删除车站英文名"},
	{PermStationGroup, "车站组", "新增 / 修改 / 删除车站组"},
	{PermSettings, "服务器配置", "查看并修改前端与寻路配置"},
	{PermAudit, "操作日志", "查看管理操作日志"},
	{PermRBAC, "权限管理", "管理角色与用户授权"},
}

// SuperAdminRole 是内置超级管理员角色名。
const SuperAdminRole = "super_admin"

var (
	ErrBuiltinRole   = errors.New("builtin role cannot be modified")
	ErrUnknownPerm   = errors.New("unknown permission")
	ErrProtectedUser = errors.New("protected super admin")
	ErrSelfLockout   = errors.New("cannot revoke own rbac permission")
	ErrBadName       = errors.New("invalid role name")
)

// Service 是 RBAC 业务逻辑。
type Service struct {
	store          *store.Store
	superAdminUUID string // 规范化（小写带横线）后的环境变量超管 UUID，可为空
	superRoleID    int64
	logger         *slog.Logger
}

// New 确保内置角色存在且权限为全集，并给环境变量指定的超管授予该角色。
func New(st *store.Store, superAdminUUID string, logger *slog.Logger) (*Service, error) {
	s := &Service{store: st, superAdminUUID: NormalizeUUID(superAdminUUID), logger: logger}
	all := permKeys()
	role, err := st.RoleByName(SuperAdminRole)
	switch {
	case errors.Is(err, store.ErrNotFound):
		id, err := st.CreateRole(SuperAdminRole, "超级管理员（内置，拥有全部权限）", true, all)
		if err != nil {
			return nil, err
		}
		s.superRoleID = id
	case err != nil:
		return nil, err
	default:
		s.superRoleID = role.ID
		// 新版本可能新增权限点：每次启动同步为全集
		if err := st.SetRolePermissions(role.ID, all); err != nil {
			return nil, err
		}
	}
	if s.superAdminUUID == "" {
		logger.Warn("BCTS_SUPER_ADMIN_UUID 未配置，没有初始超级管理员")
		return s, nil
	}
	if err := st.GrantRole(s.superAdminUUID, s.superRoleID, "env"); err != nil {
		return nil, err
	}
	logger.Info("Super admin ensured", "uuid", s.superAdminUUID)
	return s, nil
}

func permKeys() []string {
	keys := make([]string, len(AllPermissions))
	for i, p := range AllPermissions {
		keys[i] = p.Key
	}
	return keys
}

// NormalizeUUID 把 UUID 统一为小写带横线形式（接受无横线的 32 位写法）。
func NormalizeUUID(u string) string {
	u = strings.ToLower(strings.TrimSpace(u))
	if len(u) == 32 && !strings.Contains(u, "-") {
		u = u[0:8] + "-" + u[8:12] + "-" + u[12:16] + "-" + u[16:20] + "-" + u[20:]
	}
	return u
}

// Permissions 返回玩家拥有的全部权限。
func (s *Service) Permissions(uuid string) ([]string, error) {
	return s.store.PermissionsOf(NormalizeUUID(uuid))
}

// Has 判断玩家是否拥有某权限。
func (s *Service) Has(uuid, perm string) (bool, error) {
	perms, err := s.Permissions(uuid)
	if err != nil {
		return false, err
	}
	return slices.Contains(perms, perm), nil
}

// SuperRoleID 返回内置超管角色 ID。
func (s *Service) SuperRoleID() int64 { return s.superRoleID }

// SuperAdminUUID 返回环境变量指定的超管 UUID（可为空）。
func (s *Service) SuperAdminUUID() string { return s.superAdminUUID }

func validatePerms(perms []string) ([]string, error) {
	known := permKeys()
	out := make([]string, 0, len(perms))
	for _, p := range perms {
		if !slices.Contains(known, p) {
			return nil, ErrUnknownPerm
		}
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

func validName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && len([]rune(name)) <= 32 && name != SuperAdminRole
}

// CreateRole 新建自定义角色。
func (s *Service) CreateRole(name, description string, perms []string) (int64, error) {
	if !validName(name) {
		return 0, ErrBadName
	}
	perms, err := validatePerms(perms)
	if err != nil {
		return 0, err
	}
	return s.store.CreateRole(strings.TrimSpace(name), strings.TrimSpace(description), false, perms)
}

// UpdateRole 修改自定义角色。actor 若因此失去 admin.rbac 则拒绝（防止把自己锁在外面）。
func (s *Service) UpdateRole(actor string, id int64, name, description string, perms []string) error {
	role, err := s.store.RoleByID(id)
	if err != nil {
		return err
	}
	if role.Builtin {
		return ErrBuiltinRole
	}
	if !validName(name) {
		return ErrBadName
	}
	perms, err = validatePerms(perms)
	if err != nil {
		return err
	}
	if !slices.Contains(perms, PermRBAC) {
		if lose, err := s.wouldLoseRBAC(actor, id); err != nil {
			return err
		} else if lose {
			return ErrSelfLockout
		}
	}
	return s.store.UpdateRole(id, strings.TrimSpace(name), strings.TrimSpace(description), perms)
}

// DeleteRole 删除自定义角色（级联撤销授权）。
func (s *Service) DeleteRole(actor string, id int64) error {
	role, err := s.store.RoleByID(id)
	if err != nil {
		return err
	}
	if role.Builtin {
		return ErrBuiltinRole
	}
	if lose, err := s.wouldLoseRBAC(actor, id); err != nil {
		return err
	} else if lose {
		return ErrSelfLockout
	}
	return s.store.DeleteRole(id)
}

// Grant 给玩家授予角色。
func (s *Service) Grant(actor, uuid string, roleID int64) error {
	if _, err := s.store.RoleByID(roleID); err != nil {
		return err
	}
	return s.store.GrantRole(NormalizeUUID(uuid), roleID, actor)
}

// Revoke 撤销玩家的角色。环境变量超管的 super_admin 不可撤销；不能撤销自己唯一的 admin.rbac 来源。
func (s *Service) Revoke(actor, uuid string, roleID int64) error {
	uuid = NormalizeUUID(uuid)
	if roleID == s.superRoleID && uuid == s.superAdminUUID {
		return ErrProtectedUser
	}
	if uuid == NormalizeUUID(actor) {
		if lose, err := s.wouldLoseRBAC(actor, roleID); err != nil {
			return err
		} else if lose {
			return ErrSelfLockout
		}
	}
	return s.store.RevokeRole(uuid, roleID)
}

// wouldLoseRBAC 判断 actor 去掉 roleID 这个角色的 admin.rbac 后是否不再拥有 admin.rbac。
func (s *Service) wouldLoseRBAC(actor string, roleID int64) (bool, error) {
	actor = NormalizeUUID(actor)
	has, err := s.store.HasRole(actor, roleID)
	if err != nil || !has {
		return false, err
	}
	roles, err := s.store.ListRoles()
	if err != nil {
		return false, err
	}
	for _, r := range roles {
		if r.ID == roleID || !slices.Contains(r.Permissions, PermRBAC) {
			continue
		}
		if other, err := s.store.HasRole(actor, r.ID); err != nil {
			return false, err
		} else if other {
			return false, nil
		}
	}
	return true, nil
}
