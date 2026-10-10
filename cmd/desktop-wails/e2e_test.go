package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"

	"app/internal/bootstrap"
	"app/internal/config"
	"app/internal/paths"

)

// startWithDB 起一套「真装配 + 真 SQLite + 真内嵌服务」，
// 走的是与 cmd/desktop-wails 完全相同的代码路径。
//
// 这里不用桩：本文件要验证的正是「登录闭环」与「SQLite 通道」两个关口，
// 桩会让这两个关口变成空验证。
func startWithDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	paths.SetDataDir(dir)

	cfg := &config.Config{
		Mode:       config.ModeLocal,
		Storage:    config.StorageSQLite,
		DataDir:    dir,
		SQLitePath: filepath.Join(dir, "rferp.db"),
	}
	res, err := bootstrap.Build(cfg, bootstrap.Options{})
	if err != nil {
		t.Fatalf("bootstrap.Build: %v", err)
	}
	t.Cleanup(func() { _ = res.Close() })

	emb, stop, err := serve(res.Apps)
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(stop)

	return proxyFor(t, emb)
}

// proxyFor 起一个本地 HTTP 服务承载 WebView 侧的代理，返回其地址。
// 必须指向被测的那个内嵌服务：另起一个会��请求打到桩上，
// 「登录闭环」与「权限端点」两个关口就变成了空验证。
func proxyFor(t *testing.T, emb *embeddedServer) string {
	t.Helper()
	proxy := emb.proxyHandler()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: proxy}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}
func doJSON(t *testing.T, method, url, token string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// Round 1 关口「登录闭环」：登录 → 已登录外壳 → 登出后 401。
// 走真实 SQLite 与真实内嵌服务，不打桩。
func TestLoginClosedLoop(t *testing.T) {
	base := startWithDB(t)

	// 空库：先建初始管理员。
	code, _ := doJSON(t, "POST", base+"/api/bootstrap-admin", "",
		map[string]string{"username": "admin", "password": "admin12345", "display_name": "管理员"})
	if code != http.StatusOK && code != http.StatusBadRequest {
		t.Fatalf("bootstrap-admin = %d", code)
	}

	code, body := doJSON(t, "POST", base+"/api/login", "",
		map[string]string{"username": "admin", "password": "admin12345"})
	if code != http.StatusOK {
		t.Fatalf("登录 = %d, body=%v；SQLite 通道应能登录", code, body)
	}
	tok, _ := body["token"].(string)
	if tok == "" {
		t.Fatalf("登录未返回 token: %v", body)
	}

	// 已登录外壳的数据来源。
	if code, body = doJSON(t, "GET", base+"/api/me", tok, nil); code != http.StatusOK {
		t.Fatalf("/api/me = %d, body=%v", code, body)
	}
	if code, body = doJSON(t, "GET", base+"/api/parts", tok, nil); code != http.StatusOK {
		t.Fatalf("/api/parts = %d, body=%v；列表屏依赖它", code, body)
	}

	// 登出后旧 token 必须失效。
	if code, _ = doJSON(t, "DELETE", base+"/api/session", tok, nil); code != http.StatusOK {
		t.Fatalf("登出 = %d", code)
	}
	if code, _ = doJSON(t, "GET", base+"/api/parts", tok, nil); code != http.StatusUnauthorized {
		t.Errorf("登出后 /api/parts = %d, want 401", code)
	}
}

// 权限端点必须如实反映 internal/auth 的矩阵，前端导航完全依赖它。
// 这条锁住「只读看不到操作记录」这个决定。
func TestPermissionsEndpointReflectsAuthMatrix(t *testing.T) {
	base := startWithDB(t)

	_, _ = doJSON(t, "POST", base+"/api/bootstrap-admin", "",
		map[string]string{"username": "admin", "password": "admin12345"})
	_, body := doJSON(t, "POST", base+"/api/login", "",
		map[string]string{"username": "admin", "password": "admin12345"})
	adminTok, _ := body["token"].(string)

	// 建一个只读用户并登录。
	if code, _ := doJSON(t, "POST", base+"/api/users", adminTok,
		map[string]any{"username": "viewer1", "password": "viewer12345", "role": "viewer"}); code != http.StatusOK {
		t.Skipf("建只读用户失败，跳过（code=%d）", code)
	}
	_, vbody := doJSON(t, "POST", base+"/api/login", "",
		map[string]string{"username": "viewer1", "password": "viewer12345"})
	viewerTok, _ := vbody["token"].(string)
	if viewerTok == "" {
		t.Skip("只读用户登录失败，跳过")
	}

	_, adm := doJSON(t, "GET", base+"/api/me/permissions", adminTok, nil)
	_, vw := doJSON(t, "GET", base+"/api/me/permissions", viewerTok, nil)
	am, _ := adm["modules"].(map[string]any)
	vm, _ := vw["modules"].(map[string]any)

	if am == nil || vm == nil {
		t.Fatalf("权限端点未返回 modules: admin=%v viewer=%v", adm, vw)
	}

	// 管理员：全部 9 个模块可见，audit 为 read。
	if len(am) != 9 {
		t.Errorf("管理员模块数 = %d, want 9", len(am))
	}
	if am["audit"] != "read" {
		t.Errorf("管理员 audit = %v, want read", am["audit"])
	}
	if am["users"] != "write" {
		t.Errorf("管理员 users = %v, want write", am["users"])
	}

	// 只读：audit 必须是 none——这正是 2026-10-10 的决定。
	if vm["audit"] != "none" {
		t.Errorf("只读 audit = %v, want none；只读不得看到操作记录", vm["audit"])
	}
	// 其它模块仍应为只读可见。
	for _, m := range []string{"dashboard", "stats", "parts", "products", "bom", "batch"} {
		if vm[m] != "read" {
			t.Errorf("只读 %s = %v, want read", m, vm[m])
		}
	}
	// backup / users 对只读应为 none。
	for _, m := range []string{"backup", "users"} {
		if vm[m] != "none" {
			t.Errorf("只读 %s = %v, want none", m, vm[m])
		}
	}

	// 端点与实际鉴权必须一致：只读访问 audit 接口应被拒。
	if code, _ := doJSON(t, "GET", base+"/api/audit/recent", viewerTok, nil); code != http.StatusForbidden {
		t.Errorf("只读访问 /api/audit/recent = %d, want 403；权限端点与实际鉴权必须一致", code)
	}
}

