//go:build windows

package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 下载器的两条硬约束：不跟随非 https 重定向、不接收超限响应体。
//
// 这两条都不是"nice to have"：下载回来的文件随后会被执行，而 Go 的
// http.Client 默认对重定向协议不作限制；超时也只是时间上限，不是大小上限。

func TestHTTPSOnlyRedirectRejectsPlainHTTP(t *testing.T) {
	req, err := http.NewRequest("GET", "http://example.invalid/x.exe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpsOnlyRedirect(req, nil); err == nil {
		t.Fatal("允许了 https -> http 的降级重定向")
	}
}

func TestHTTPSOnlyRedirectAllowsHTTPS(t *testing.T) {
	req, err := http.NewRequest("GET", "https://example.invalid/x.exe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpsOnlyRedirect(req, nil); err != nil {
		t.Fatalf("拒绝了正常的 https 重定向: %v", err)
	}
}

func TestHTTPSOnlyRedirectHasHopLimit(t *testing.T) {
	req, err := http.NewRequest("GET", "https://example.invalid/x.exe", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpsOnlyRedirect(req, make([]*http.Request, 5)); err == nil {
		t.Fatal("重定向次数没有上限")
	}
}

// downloadTo 在响应体超过上限时必须报错，而不是把整段写进磁盘。
func TestDownloadToEnforcesLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("A", 4096)))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "out.bin")
	err := downloadTo(srv.Client(), srv.URL, dst, 1024)
	if err == nil {
		t.Fatal("超过上限的响应被接受了")
	}
	if !strings.Contains(err.Error(), "上限") {
		t.Errorf("错误信息没说清是超限: %v", err)
	}
}

// 正常大小应当成功，而且落盘内容完整 —— 否则上一条可能只是因为"全都失败"才通过。
func TestDownloadToWritesWhenWithinLimit(t *testing.T) {
	payload := strings.Repeat("B", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(payload))
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "out.bin")
	if err := downloadTo(srv.Client(), srv.URL, dst, 1<<20); err != nil {
		t.Fatalf("正常下载失败: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != payload {
		t.Errorf("落盘内容长度 %d，期望 %d", len(got), len(payload))
	}
}

// 非 200 必须失败（3xx/4xx 都可能出现在配置错误的代理后面）。
func TestDownloadToRejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dst := filepath.Join(t.TempDir(), "out.bin")
	if err := downloadTo(srv.Client(), srv.URL, dst, 1<<20); err == nil {
		t.Fatal("404 被当成了正常响应")
	}
}
