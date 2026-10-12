package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/api"
	"app/internal/config"
)

// 首启阶段的 handler 是 cmd 侧自建的 mux，不经过 Server.Handler()，因此不会
// 自动继承启动凭据闸门。曾经直接 `http.Server{Handler: backend}`，结果在
// 首启窗口打开期间，本机任何进程都能打 /api/setup/*：
//
//	POST /api/setup/apply        → 改写配置（改指自己的 MySQL、设已知密码）
//	POST /api/setup/create-admin → 建管理员
//	POST /api/setup/start-mysql-service → 启停服务
//
// 首启窗口宽度只有 720，用户不会察觉后台发生了这些事。
func TestFirstRunSetupHandlerIsBehindStartupGate(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef"

	// 与 runFirstRunSetup 里完全相同的组装方式
	backend := newSwappableBackend(http.NotFoundHandler())
	backend.swap(newSetupHandler(config.Load(), func() {}))

	// 无凭据
	for _, path := range []string{"/api/setup/defaults", "/api/setup/apply"} {
		req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		api.StartupGate(token, backend).ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s 无启动凭据时返回 %d，期望 403", path, w.Code)
		}
	}

	// 页面路径同样受保护：只锁 API 会产生「页面进不去但接口能打」的半可用状态
	w := httptest.NewRecorder()
	api.StartupGate(token, backend).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusForbidden {
		t.Errorf("GET / 无启动凭据时返回 %d，期望 403", w.Code)
	}

	// 带正确凭据则放行（WebView 由 embeddedServer 注入 Cookie）
	req := httptest.NewRequest("GET", "/api/setup/defaults", nil)
	req.AddCookie(&http.Cookie{Name: api.StartupCookieName, Value: token})
	w = httptest.NewRecorder()
	api.StartupGate(token, backend).ServeHTTP(w, req)
	if w.Code == http.StatusForbidden {
		t.Error("带正确启动凭据仍被 403：首启界面会被自己的闸门锁死")
	}
}

// create-admin 本身还应拒绝「已有用户」，这样即便闸门被绕过也无法重放建号。
func TestCreateFirstAdminRefusesWhenUsersExist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("LOCALAPPDATA", dir)

	if err := createFirstAdmin(nil, "attacker", "Password123", ""); err == nil {
		t.Fatal("空库下 createFirstAdmin 失败：测试前提不成立")
	}
	if err := createFirstAdmin(nil, "attacker2", "Password123", ""); err == nil {
		t.Fatal("已有用户时 createFirstAdmin 仍成功：可被重放建号")
	}
}
