package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/api"
	"app/internal/usecase"
)

// stubApps 只实现 /healthz 之外的断言所需能力；本文件验证的是启动网关，
// 不触碰业务用例，因此嵌入 nil 接口即可。
type stubApps struct{ usecase.Applications }

// startEmbedded 起一个真实的内嵌服务（随机回环端口），返回地址与代理。
func startEmbedded(t *testing.T) (*embeddedServer, string, http.Handler) {
	t.Helper()
	emb, stop, err := serve(&stubApps{})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(stop)
	return emb, "http://" + emb.addr, emb.proxyHandler()
}

// Round 1 关口「启动 token」的核心断言：
// 同机进程直连该端口必须被拒，而经由 AssetServer 代理的 WebView 请求必须放行。
func TestDirectAccessWithoutCookieIsRejected(t *testing.T) {
	_, base, _ := startEmbedded(t)

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("无 Cookie 直连 = %d, want 403；这是「无 token 的同机请求被拒」关口", resp.StatusCode)
	}
}

// 经代理的请求（模拟 WebView）必须放行——代理负责补 Cookie。
func TestProxiedRequestIsAllowed(t *testing.T) {
	_, _, proxy := startEmbedded(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/healthz", nil)
	proxy.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("经代理请求 /healthz = %d, want 200；代理未补上启动 Cookie", w.Code)
	}
	if body, _ := io.ReadAll(w.Result().Body); !strings.Contains(string(body), "ok") {
		t.Errorf("响应体异常: %q", string(body))
	}
}

// token 全程不下发给浏览器：代理不得把 Cookie 写进响应头，
// 否则它会落盘并可被前端 JS 读到。
func TestProxyDoesNotLeakTokenToBrowser(t *testing.T) {
	_, _, proxy := startEmbedded(t)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/healthz", nil)
	proxy.ServeHTTP(w, r)

	if n := len(w.Result().Cookies()); n != 0 {
		t.Errorf("代理下发了 %d 个 Set-Cookie；启动凭据不应进入浏览器", n)
	}
}

// 前端伪造同名 Cookie 时，代理不应被绕过逻辑改写——
// 内嵌服务仍会校验其真伪，伪造值拿不到访问权。
func TestForgedCookieIsStillRejectedUpstream(t *testing.T) {
	emb, base, _ := startEmbedded(t)

	req, _ := http.NewRequest("GET", base+"/healthz", nil)
	req.AddCookie(&http.Cookie{Name: api.StartupCookieName, Value: strings.Repeat("a", 48)})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("伪造 Cookie = %d, want 403", resp.StatusCode)
	}
	_ = emb
}

// 端口必须是随机分配的回环端口，不能固定、也不能监听 0.0.0.0。
func TestEmbeddedBindsRandomLoopbackPort(t *testing.T) {
	emb, _, _ := startEmbedded(t)

	if !strings.HasPrefix(emb.addr, "127.0.0.1:") {
		t.Errorf("addr = %q, want 127.0.0.1:...；只绑回环，不监听 0.0.0.0", emb.addr)
	}
	if emb.addr == "127.0.0.1:0" {
		t.Error("端口未分配")
	}
}

// 两次启动应拿到不同端口与不同 token。
func TestEachStartGetsDistinctPortAndToken(t *testing.T) {
	a, _, _ := startEmbedded(t)
	b, _, _ := startEmbedded(t)

	if a.addr == b.addr {
		t.Errorf("两次启动端口相同: %s", a.addr)
	}
	if a.token == b.token {
		t.Error("两次启动 token 相同；token 必须每次随机")
	}
	if len(a.token) != 48 {
		t.Errorf("token 长度 = %d, want 48（24 字节 hex）", len(a.token))
	}
}

// 停止后端口应当释放，否则反复启停会耗尽端口。
func TestStopReleasesPort(t *testing.T) {
	emb, stop, err := serve(&stubApps{})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	addr := emb.addr

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("停止前请求失败: %v", err)
	}
	resp.Body.Close()

	stop()

	// 停止后再连应当失败（连接被拒绝）。
	if c, err := http.Get("http://" + addr + "/healthz"); err == nil {
		c.Body.Close()
		t.Error("停止后端口仍可连接；清理不可靠")
	}
}
