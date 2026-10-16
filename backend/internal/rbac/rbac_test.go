package rbac

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"slices"
	"testing"

	"railway-map-backend/internal/store"
)

const (
	superUUID = "11111111-2222-3333-4444-555555555555"
	otherUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func newTestService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open("sqlite", "", filepath.Join(t.TempDir(), "rbac.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	// 无横线大写写法也应被规范化
	s, err := New(st, "1111111122223333444455555555555"+"5", slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return s, st
}

func TestSuperAdminBootstrap(t *testing.T) {
	s, _ := newTestService(t)
	perms, err := s.Permissions(superUUID)
	if err != nil {
		t.Fatal(err)
	}
	if len(perms) != len(AllPermissions) {
		t.Errorf("super admin perms = %v", perms)
	}
	if err := s.Revoke(superUUID, superUUID, s.SuperRoleID()); !errors.Is(err, ErrProtectedUser) {
		t.Errorf("revoke env super admin err = %v, want ErrProtectedUser", err)
	}
	if err := s.DeleteRole(superUUID, s.SuperRoleID()); !errors.Is(err, ErrBuiltinRole) {
		t.Errorf("delete builtin err = %v", err)
	}
}

func TestCustomRoleGrantRevoke(t *testing.T) {
	s, _ := newTestService(t)
	id, err := s.CreateRole("站务", "", []string{PermStationAlias, PermStationAlias})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRole("bad", "", []string{"nope"}); !errors.Is(err, ErrUnknownPerm) {
		t.Errorf("unknown perm err = %v", err)
	}
	if err := s.Grant(superUUID, otherUUID, id); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Has(otherUUID, PermStationAlias); !ok {
		t.Error("granted perm missing")
	}
	if ok, _ := s.Has(otherUUID, PermRBAC); ok {
		t.Error("unexpected perm")
	}
	if err := s.Revoke(superUUID, otherUUID, id); err != nil {
		t.Fatal(err)
	}
	if perms, _ := s.Permissions(otherUUID); len(perms) != 0 {
		t.Errorf("perms after revoke = %v", perms)
	}
}

// 唯一的 admin.rbac 来源不能被自己撤销 / 删除 / 去掉该权限。
func TestSelfLockoutPrevented(t *testing.T) {
	s, _ := newTestService(t)
	id, _ := s.CreateRole("管理员", "", []string{PermRBAC})
	_ = s.Grant(superUUID, otherUUID, id)
	if err := s.Revoke(otherUUID, otherUUID, id); !errors.Is(err, ErrSelfLockout) {
		t.Errorf("self revoke err = %v", err)
	}
	if err := s.UpdateRole(otherUUID, id, "管理员", "", []string{PermAudit}); !errors.Is(err, ErrSelfLockout) {
		t.Errorf("self update err = %v", err)
	}
	if err := s.DeleteRole(otherUUID, id); !errors.Is(err, ErrSelfLockout) {
		t.Errorf("self delete err = %v", err)
	}
	// 他人（超管）可以操作
	if err := s.UpdateRole(superUUID, id, "审计员", "", []string{PermAudit}); err != nil {
		t.Fatal(err)
	}
	perms, _ := s.Permissions(otherUUID)
	if !slices.Equal(perms, []string{PermAudit}) {
		t.Errorf("perms = %v", perms)
	}
}
