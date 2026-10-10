package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const bootTok = "0123456789abcdef0123456789abcdef0123456789abcdef"

func gatedServer(t *testing.T, tok string) *Server {
	t.Helper()
	return New(&sessionApps{}, WithStartupToken(tok))
}

func do(s *Server, path, cookieVal string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	if cookieVal != "" {
		r.AddCookie(&http.Cookie{Name: startupCookieName, Value: cookieVal})
	}
	s.Handler().ServeHTTP(w, r)
	return w
}

// 无 token 的请求必须被拒——这就是「无 token 的同机请求被拒」这条关口。
func TestStartupGateRejectsRequestWithoutToken(t *testing.T) {
	s := gatedServer(t, bootTok)
	if w := do(s, "/api/users/count", ""); w.Code != http.StatusForbidden {
		t.Errorf("无 token 请求 = %d, want 403", w.Code)
	}
}

func TestStartupGateRejectsWrongToken(t *testing.T) {
	s := gatedServer(t, bootTok)

	cases := map[string]string{
		"完全不同":   strings.Repeat("f", len(bootTok)),
		"正确值少一位": bootTok[:len(bootTok)-1],
		"正确值多一位": bootTok + "0",
		"大小写不同":  strings.ToUpper(bootTok),
		"空字符串":   "",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			if w := do(s, "/api/users/count", tok); w.Code != http.StatusForbidden {
				t.Errorf("token=%q -> %d, want 403", tok, w.Code)
			}
		})
	}
}

// 持有正确 Cookie 必须放行。
// 用 /healthz：它不触碰 apps，本文件的桩是 nil 嵌入，无需实现任何方法。
func TestStartupCookieUnlocksAPI(t *testing.T) {
	s := gatedServer(t, bootTok)
	if w := do(s, "/healthz", bootTok); w.Code != http.StatusOK {
		t.Errorf("持有正确 Cookie 访问 /healthz = %d, want 200", w.Code)
	}
}

// 未配置启动 token 时（cmd/server），网关必须恒等，不能误伤。
func TestNoStartupTokenMeansNoGate(t *testing.T) {
	s := New(&sessionApps{})
	if w := do(s, "/healthz", ""); w.Code != http.StatusOK {
		t.Errorf("未配置启动 token 时 /healthz = %d, want 200；cmd/server 走 Bearer 认证，网关须恒等", w.Code)
	}
}

// 根路径也受门保护：没有 Cookie 直接访问 "/" 不应拿到页面。
func TestRootPathRequiresToken(t *testing.T) {
	s := gatedServer(t, bootTok)
	s.staticFS, s.staticDir = os.DirFS(t.TempDir()), "." // 空目录，静态资源必然缺失

	if w := do(s, "/", ""); w.Code != http.StatusForbidden {
		t.Errorf("无 token 访问根路径 = %d, want 403", w.Code)
	}
}

// 静态资源与 API 受同一条门保护，避免「页面进不去但接口能打」。
func TestStaticAssetsAlsoGated(t *testing.T) {
	s := gatedServer(t, bootTok)
	s.staticFS, s.staticDir = os.DirFS(t.TempDir()), "."

	for _, path := range []string{"/index.html", "/assets/app.js", "/api/parts", "/healthz"} {
		if w := do(s, path, ""); w.Code != http.StatusForbidden {
			t.Errorf("%s 无 token = %d, want 403", path, w.Code)
		}
	}
}

// 单页回退不得吞掉 /api/ 的 404：否则前端会把「接口不存在」渲染成空白页。
func TestSPAFallbackDoesNotSwallowAPINotFound(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/index.html", []byte("<html>app</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(&sessionApps{}, WithStaticFS(os.DirFS(dir), "."))

	if w := do(s, "/api/does-not-exist", ""); w.Code != http.StatusNotFound {
		t.Errorf("未知 /api/ 路径 = %d, want 404，不应回落到 index.html", w.Code)
	}
	if w := do(s, "/some/spa/route", ""); w.Code != http.StatusOK {
		t.Errorf("前端路由 /some/spa/route = %d, want 200（应回落到 index.html）", w.Code)
	}
}

// 前端资源未构建时给出可执行的指引，而不是裸 404。
func TestMissingBuildGivesActionableHint(t *testing.T) {
	s := New(&sessionApps{}, WithStaticFS(os.DirFS(t.TempDir()), "."))

	w := do(s, "/", "")
	if w.Code != http.StatusNotFound {
		t.Fatalf("资源缺失 = %d, want 404", w.Code)
	}
	if !strings.Contains(w.Body.String(), "npm run build") {
		t.Errorf("未给出修复指引: %q", w.Body.String())
	}
}
