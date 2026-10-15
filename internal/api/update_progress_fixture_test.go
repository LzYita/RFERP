package api

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"app/internal/update"
)

// 异步下载测试的脚手架：一个**本地**清单服务器 + 一份用测试密钥签名的清单。
//
// 两点必须做对，否则测试会假绿：
//
//  1. **签名走生产代码**。清单用 `update.Manifest.Sign` 签，不自己拼 JSON ——
//     自己拼一份签名逻辑等于把"签名覆盖哪些字段"抄了第二遍，改一处另一处不动，
//     测试照样绿。
//  2. **不碰生产私钥**。验签用的是 `internal/update` 的包级 `PublicKeyB64`，
//     它是 var，测试临时换成当场生成的密钥对并还原。验签的**代码路径**是真的，
//     只是换了一把钥匙。
//
// swapManifestKey 临时把验签公钥换成测试密钥对，返回还原函数。
//
// `update.PublicKeyB64` 是包级变量（不是 per-Server 的），所以替换是全局的。
// 这里只在**替换与还原的那一瞬间**加锁；本包的测试没有 t.Parallel()，Go 默认
// 串行跑用例，因此持有锁期间不会被另一个用例观察到不一致。若将来有人给某个
// 用例加 t.Parallel()，这条锁**救不了**（语义上的竞态，不是数据竞态）——那时必须
// 把公钥做成 per-Server 注入项，而不是加锁。
var manifestKeyMu sync.Mutex

func swapManifestKey(t *testing.T, pub ed25519.PublicKey) {
	t.Helper()
	manifestKeyMu.Lock()
	defer manifestKeyMu.Unlock()
	old := update.PublicKeyB64
	update.PublicKeyB64 = base64.StdEncoding.EncodeToString(pub)
	t.Cleanup(func() {
		manifestKeyMu.Lock()
		defer manifestKeyMu.Unlock()
		update.PublicKeyB64 = old
	})
}

func startSignedManifestServer(t *testing.T, version string, payload []byte) (string, func()) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	swapManifestKey(t, pub)

	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/package.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", string(rune(len(payload))))
		_, _ = w.Write(payload)
	})
	srv = httptest.NewServer(mux)

	sum := sha256.Sum256(payload)
	m := &update.Manifest{
		Version: version,
		URL:     srv.URL + "/package.bin",
		SHA256:  hex.EncodeToString(sum[:]),
		Size:    int64(len(payload)),
	}
	if err := m.Sign(priv); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	mux.HandleFunc("/releases.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
	return srv.URL + "/releases.json", srv.Close
}

// fakePackage 造一段非空字节当作"更新包"。
//
// 刻意**不做成 zip**：异步下载这条链只做"下载 + 核对哈希"，解压发生在 apply 阶段，
// 而 apply 的用例用的是另一个真 zip 助手（seedZip）。这里字节流一致就够了。
func fakePackage() []byte {
	return []byte("RFERP-async-download-payload-do-not-parse")
}

// waitProgress 轮询到任务离开 running 状态，或超时后返回最后一次看到的。
func waitProgress(t *testing.T, s *Server, tok, jobID string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		w := doJSON(t, s, "GET", "/api/update/progress?job="+jobID, tok, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("进度查询 = %d，期望 200（%s）", w.Code, w.Body.String())
		}
		var out map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("进度响应不是 JSON: %v", err)
		}
		if out["state"] != "running" || time.Now().After(deadline) {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func decodeBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON: %v（%s）", err, w.Body.String())
	}
	return out
}
