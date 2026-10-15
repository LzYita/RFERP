package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 先决项⑤：**「验签被拒」不得伪装成「没有更新」**。
//
// 背景（真实发生过）：`Check` 会先试配置源、再试内置回退源，取第一个成功的结果。
// 配置源被生产公钥拒 → 回退源成功且没有更高版本 → 净结果是"没有更新"，而那条验签
// 失败被**彻底丢掉**。于是发版时签错密钥这类事故表现为"所有用户静默停止更新、
// 界面一律显示已是最新、没有任何人报错"。
//
// 这组测试锁三件事：
//  1. 验签被拒时，即使净结果是"没有更新"，也必须能从 Result 里读出来（Warning 非空）；
//  2. 全部失败时，返回的错误是**被拒**那条，而不是回退源的连接失败；
//  3. 正常的"连不上就回退"路径**不**产生警告（否则告警会被日常噪音淹掉）。
func signedManifest(t *testing.T, priv ed25519.PrivateKey, version, pkgURL string) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte("payload"))
	m := Manifest{
		Version: version,
		URL:     pkgURL,
		Size:    int64(len("payload")),
		SHA256:  hex.EncodeToString(sum[:]),
	}
	if err := m.Sign(priv); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// 场景 1：配置源被验签拒 + 回退源正常（且没有更高版本）。
// 净结果确实是"没有更新"，但"配置源被拒"这件事必须可见。
func TestCheckReportsRejectionEvenWhenNothingNewer(t *testing.T) {
	_, wrongPriv, _ := ed25519.GenerateKey(rand.Reader) // 用它签 → 生产公钥认不出
	prodPub, prodPriv, _ := ed25519.GenerateKey(rand.Reader)

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(signedManifest(t, wrongPriv, "9.9.9", "http://127.0.0.1:1/p.zip"))
	}))
	defer bad.Close()

	// 回退源：签名正确，但版本不比当前高 → 正常情况
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(signedManifest(t, prodPriv, "1.0.0", "http://127.0.0.1:1/p.zip"))
	}))
	defer fallback.Close()

	c := &Checker{ManifestURL: bad.URL, FallbackURL: fallback.URL,
		CurrentVersion: "1.0.0", PublicKey: prodPub, Client: bad.Client()}
	res, err := c.Check()
	if err != nil {
		t.Fatalf("回退源可用，不该整体报错: %v", err)
	}
	if res.Manifest != nil {
		t.Fatal("回退源的版本不比当前高，不该判定为有更新")
	}
	// 这三条就是本条修复的全部意义
	if w := res.Warning(); w == "" {
		t.Fatal("配置源被验签拒，却没有任何 warning：这正是'伪装成没有更新'")
	}
	if len(res.Attempts) != 2 {
		t.Fatalf("应记录两次尝试，实际 %d", len(res.Attempts))
	}
	if res.Attempts[0].Status != AttemptRejected {
		t.Errorf("配置源的状态 = %s，期望 rejected", res.Attempts[0].Status)
	}
	if res.Attempts[1].Status != AttemptNotNewer {
		t.Errorf("回退源的状态 = %s，期望 not-newer", res.Attempts[1].Status)
	}
	// warning 里要能看出是哪个地址、为什么
	if !strings.Contains(res.Warning(), bad.URL) {
		t.Errorf("warning 里没有指出是哪个源: %s", res.Warning())
	}
}

// 场景 2：两个源都失败，一个是"验签被拒"、一个是"连不上"。
// 返回的错误必须是**被拒**那条 —— 否则一次发版事故看起来像网络抖动，排查方向被带偏。
func TestCheckPrefersRejectionOverUnreachable(t *testing.T) {
	_, wrongPriv, _ := ed25519.GenerateKey(rand.Reader)
	prodPub, _, _ := ed25519.GenerateKey(rand.Reader)

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(signedManifest(t, wrongPriv, "9.9.9", "http://127.0.0.1:1/p.zip"))
	}))
	defer bad.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // 立刻关掉 → 连接被拒

	c := &Checker{ManifestURL: bad.URL, FallbackURL: deadURL,
		CurrentVersion: "1.0.0", PublicKey: prodPub, Client: bad.Client()}
	res, err := c.Check()
	if err == nil {
		t.Fatal("两个源都失败，应当返回错误")
	}
	var rejected *RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("返回的错误 = %v（类型 %T），期望是先被验签拒的那条", err, err)
	}
	if res.Warning() == "" {
		t.Error("有源被验签拒，warning 不该为空")
	}
}

// 场景 3：正常的容错路径（配置源连不上、回退源正常且没新版）**不该**报警。
// 没有这一条，上面的警告会被"每次配置源抖一下都告警"淹没，等于没修。
func TestCheckFallbackWithoutRejectionIsNotAWarning(t *testing.T) {
	prodPub, prodPriv, _ := ed25519.GenerateKey(rand.Reader)

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(signedManifest(t, prodPriv, "1.0.0", "http://127.0.0.1:1/p.zip"))
	}))
	defer fallback.Close()

	c := &Checker{ManifestURL: deadURL, FallbackURL: fallback.URL,
		CurrentVersion: "1.0.0", PublicKey: prodPub, Client: fallback.Client()}
	res, err := c.Check()
	if err != nil {
		t.Fatalf("回退源可用，不该报错: %v", err)
	}
	if w := res.Warning(); w != "" {
		t.Errorf("只是配置源连不上，不该产生 warning（会淹没真正的告警）: %s", w)
	}
	if res.Attempts[0].Status != AttemptUnreachable {
		t.Errorf("配置源状态 = %s，期望 unreachable", res.Attempts[0].Status)
	}
}

// 场景 4：清单验签通过但不可用（缺下载地址）→ 也算"源告诉我们发布有问题"，要留痕。
func TestCheckReportsUnusableManifest(t *testing.T) {
	prodPub, prodPriv, _ := ed25519.GenerateKey(rand.Reader)

	// 签名正确、版本更高，但缺 URL/SHA256
	sum := sha256.Sum256([]byte("x"))
	_ = sum
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := Manifest{Version: "2.0.0"}
		if err := m.Sign(prodPriv); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(m)
	}))
	defer srv.Close()

	c := &Checker{ManifestURL: srv.URL, CurrentVersion: "1.0.0",
		PublicKey: prodPub, Client: srv.Client()}
	res, err := c.Check()
	if err == nil {
		t.Fatal("清单缺字段，应当返回错误")
	}
	if res.Attempts[0].Status != AttemptUnusable {
		t.Errorf("状态 = %s，期望 unusable", res.Attempts[0].Status)
	}
	if res.Warning() == "" {
		t.Error("清单不可用应当产生 warning")
	}
}
