package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"railway-map-backend/internal/auth"
	"railway-map-backend/internal/config"
	"railway-map-backend/internal/geo"
	"railway-map-backend/internal/model"
	"railway-map-backend/internal/pluginconfig"
	"railway-map-backend/internal/rbac"
	"railway-map-backend/internal/settings"
	"railway-map-backend/internal/store"
)

const (
	uCreator = "00000000-0000-0000-0000-000000000001"
	uMember  = "00000000-0000-0000-0000-000000000002"
	uOutside = "00000000-0000-0000-0000-000000000003"
	uAdmin   = "00000000-0000-0000-0000-000000000009"
)

// fakePlugin 模拟插件：记录收到的 config.request 并立即回执成功。
type fakePlugin struct {
	req    *pluginconfig.Requester
	online bool
	ops    []string
}

func (f *fakePlugin) Online() bool { return f.online }

func (f *fakePlugin) Send(env model.Envelope) bool {
	var wire model.ConfigRequestWire
	_ = json.Unmarshal(env.Data, &wire)
	f.ops = append(f.ops, wire.Op)
	go f.req.Deliver(model.ConfigResult{RequestID: wire.RequestID, Success: true})
	return true
}

type testEnv struct {
	router http.Handler
	auth   *auth.Service
	plugin *fakePlugin
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	st, err := store.Open("sqlite", "", filepath.Join(t.TempDir(), "center.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cache := geo.NewCache(st)
	systems, _ := json.Marshal([]model.PrivateSystem{{ID: "sys", Name: "S", Creator: uCreator, Members: []string{uCreator, uMember}}})
	lines, _ := json.Marshal([]model.PrivateLine{{ID: "l1", SystemID: "sys", Name: "L", Color: "#ffffff", BossbarStations: []string{"A", "B"}}})
	if err := cache.SetPrivateSystems(systems); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetPrivateLines(lines); err != nil {
		t.Fatal(err)
	}
	rb, err := rbac.New(st, uAdmin, logger)
	if err != nil {
		t.Fatal(err)
	}
	set, err := settings.New(st, config.LegacySections{}, logger)
	if err != nil {
		t.Fatal(err)
	}
	authSvc := auth.NewSessionService("test-secret", st)
	fp := &fakePlugin{online: true}
	fp.req = pluginconfig.New(fp, time.Second)
	api := New(Options{Auth: authSvc, Frontend: set.Frontend, Logger: logger})
	c := NewCenter(CenterOptions{
		API: api, Store: st, Cache: cache, RBAC: rb, Settings: set,
		Plugin: fp.req, PluginOnline: fp.Online, Logger: logger,
	})
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(c.RequireLogin)
		r.Get("/me/profile", c.Profile)
		r.Put("/me/systems/{id}", c.UpdateSystem)
		r.Delete("/me/systems/{id}", c.DeleteSystem)
		r.Put("/me/lines/{lineId}", c.UpdateLine)
		r.With(c.RequirePerm(rbac.PermSettings)).Get("/admin/settings", c.GetSettings)
	})
	return &testEnv{router: r, auth: authSvc, plugin: fp}
}

// do 以 uuid 身份（空则未登录）发请求，返回状态码。
func (e *testEnv) do(t *testing.T, uuid, method, path, body string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if uuid != "" {
		rec := httptest.NewRecorder()
		if err := e.auth.IssueCookie(rec, &model.Player{UUID: uuid, Name: "p"}); err != nil {
			t.Fatal(err)
		}
		for _, ck := range rec.Result().Cookies() {
			req.AddCookie(ck)
		}
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec.Code
}

func TestCenterPermissionMatrix(t *testing.T) {
	e := newTestEnv(t)
	sysBody := `{"name":"S2","pricePerKm":0.3}`
	membersBody := `{"name":"S2","members":["` + uMember + `"]}`
	lineBody := `{"systemId":"sys","name":"L2","color":"#000000","bossbarStations":["A","B"]}`
	cases := []struct {
		name, uuid, method, path, body string
		want                           int
	}{
		{"未登录", "", http.MethodGet, "/me/profile", "", 401},
		{"成员可编辑系统", uMember, http.MethodPut, "/me/systems/sys", sysBody, 200},
		{"成员不能改成员", uMember, http.MethodPut, "/me/systems/sys", membersBody, 403},
		{"创建者可改成员", uCreator, http.MethodPut, "/me/systems/sys", membersBody, 200},
		{"路人看不到系统", uOutside, http.MethodPut, "/me/systems/sys", sysBody, 404},
		{"成员不能删除系统", uMember, http.MethodDelete, "/me/systems/sys", "", 403},
		{"管理员可删除系统", uAdmin, http.MethodDelete, "/me/systems/sys", "", 200},
		{"创建者可删除系统", uCreator, http.MethodDelete, "/me/systems/sys", "", 200},
		{"成员可编辑线路", uMember, http.MethodPut, "/me/lines/l1", lineBody, 200},
		{"路人不能编辑线路", uOutside, http.MethodPut, "/me/lines/l1", lineBody, 404},
		{"线路颜色校验", uMember, http.MethodPut, "/me/lines/l1", strings.Replace(lineBody, "#000000", "red", 1), 400},
		{"不能编辑不存在的系统", uAdmin, http.MethodPut, "/me/systems/nope", sysBody, 404},
		{"无权限看服务器配置", uCreator, http.MethodGet, "/admin/settings", "", 403},
		{"超管看服务器配置", uAdmin, http.MethodGet, "/admin/settings", "", 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := e.do(t, tc.uuid, tc.method, tc.path, tc.body); got != tc.want {
				t.Errorf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPluginOffline(t *testing.T) {
	e := newTestEnv(t)
	e.plugin.online = false
	if got := e.do(t, uMember, http.MethodPut, "/me/systems/sys", `{"name":"S2"}`); got != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", got)
	}
	if len(e.plugin.ops) != 0 {
		t.Errorf("ops sent while offline: %v", e.plugin.ops)
	}
}
