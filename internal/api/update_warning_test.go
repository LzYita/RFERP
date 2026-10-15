package api

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"app/internal/update"
)

// 先决项⑤ 的 API 层：**「验签被拒」不得表现成「已是最新版本」**。
//
// 单测（internal/update）已确认 Check 不丢拒绝事件；这里确认它一路走到 HTTP 响应里 ——
// 否则设置屏仍然只能说"已是最新版本"，事故依然不可见。
//
// 两条源都指向本地：真实 checker 的回退地址是**线上 GitHub**（本机不通会卡 60 秒、
// CI 上通但结果不同），所以用 WithUpdateChecker 注入。
const pkgSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func manifestServer(t *testing.T, signPriv ed25519.PrivateKey, version string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := update.Manifest{
			Version: version,
			URL:     "http://127.0.0.1:1/pkg.zip",
			Size:    10,
			SHA256:  pkgSHA,
		}
		if err := m.Sign(signPriv); err != nil {
			t.Error(err)
		}
		_ = json.NewEncoder(w).Encode(m)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func checkServer(t *testing.T, pinned ed25519.PublicKey, configured, fallback *httptest.Server) *Server {
	t.Helper()
	return New(&sessionApps{},
		WithUpdate("http://configured.invalid/releases.json", "1.0.0"),
		WithUpdateChecker(func(url, current string) (*update.Checker, error) {
			return &update.Checker{
				ManifestURL:    configured.URL,
				FallbackURL:    fallback.URL,
				CurrentVersion: current,
				PublicKey:      pinned,
				Client:         configured.Client(),
			}, nil
		}))
}

// 配置源被验签拒 + 回退源正常且没有更高版本 → 200，但必须带出"配置源被拒"。
func TestCheckEndpointSurfacesRejectedSource(t *testing.T) {
	pinnedPub, pinnedPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	bad := manifestServer(t, wrongPriv, "9.9.9")   // 签名对不上 → 被拒
	good := manifestServer(t, pinnedPriv, "1.0.0") // 对得上但版本不更高

	s := checkServer(t, pinnedPub, bad, good)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "GET", "/api/update/check", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("check = %d，期望 200（回退源可用，不该整体报错）：%s", w.Code, w.Body.String())
	}
	out := decodeBody(t, w)
	if out["hasUpdate"] != false {
		t.Errorf("回退源没有更高版本，hasUpdate 应为 false，实际 %v", out["hasUpdate"])
	}
	warn, _ := out["warning"].(string)
	if warn == "" {
		t.Fatal("配置源被验签拒，响应里却没有 warning：界面只能显示'已是最新版本'，" +
			"这正是先决项⑤要修的那种伪装")
	}
	if !strings.Contains(warn, bad.URL) {
		t.Errorf("warning 没指出是哪个源：%s", warn)
	}
}

// 两条源都正常 → 不出现 warning（否则告警会被正常情况淹没，等于没修）。
func TestCheckEndpointHasNoWarningOnHealthyPath(t *testing.T) {
	pinnedPub, pinnedPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	good := manifestServer(t, pinnedPriv, "1.0.0")

	s := checkServer(t, pinnedPub, good, good)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "GET", "/api/update/check", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("check = %d，期望 200", w.Code)
	}
	if warn := decodeBody(t, w)["warning"]; warn != nil {
		t.Errorf("正常路径不该带 warning：%v", warn)
	}
}

// 下载端点也要把原因说清楚：没有可下载版本时，若是"源被拒"，不能只说"没有可下载的新版本"。
func TestDownloadEndpointExplainsRejection(t *testing.T) {
	pinnedPub, pinnedPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, wrongPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bad := manifestServer(t, wrongPriv, "9.9.9")
	good := manifestServer(t, pinnedPriv, "1.0.0")

	s := checkServer(t, pinnedPub, bad, good)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/update/download?async=1", tok, nil)
	if w.Code == http.StatusAccepted {
		t.Fatal("没有更高版本，不该受理下载")
	}
	body := w.Body.String()
	if !strings.Contains(body, "未通过校验") {
		t.Errorf("400 的文案没说明是验签问题，调用方会以为只是没有新版：%s", body)
	}
}
