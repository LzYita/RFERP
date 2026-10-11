package main

import (
	"net"
	"strings"
	"testing"
)

// 端口占用必须给出可操作的提示，而不是只丢一句 address already in use。
//
// 撞端口只可能发生在 -serve 模式（固定端口）；窗口模式用 :0 让内核分配，
// 永远不会撞。开发期撞上时，用户需要知道「换一个端口」，否则只能干瞪眼。
func TestFixedPortConflictGivesActionableError(t *testing.T) {
	// 先占住一个端口
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败: %v", err)
	}
	defer blocker.Close()
	addr := blocker.Addr().String()

	_, _, err = serveOn(&stubApps{}, addr)
	if err == nil {
		t.Fatal("端口被占用时 serveOn 应当失败")
	}

	msg := err.Error()
	for _, want := range []string{"已被占用", "-serve-addr", "127.0.0.1:"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息缺少 %q：\n%s", want, msg)
		}
	}
}

// 建议的替代端口必须真的是空的，否则用户照着改还是起不来。
func TestSuggestedPortIsActuallyFree(t *testing.T) {
	sugg := randomPortSuggestion("127.0.0.1:1")
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(sugg)))
	if err != nil {
		t.Fatalf("建议的端口 %d 不可用: %v", sugg, err)
	}
	ln.Close()
}

// 窗口模式用 :0，连续启动不应撞端口——这正是选随机端口的理由。
func TestKernelAllocatedPortNeverConflicts(t *testing.T) {
	var addrs []string
	for i := 0; i < 5; i++ {
		emb, stop, err := serveOn(&stubApps{}, "127.0.0.1:0")
		if err != nil {
			t.Fatalf("第 %d 次启动失败: %v", i, err)
		}
		addrs = append(addrs, emb.addr)
		stop() // 不释放也不该影响下一次分配
	}
	seen := map[string]bool{}
	for _, a := range addrs {
		if seen[a] {
			t.Errorf("两次启动拿到同一端口 %s", a)
		}
		seen[a] = true
	}
	for _, a := range addrs {
		if !strings.HasPrefix(a, "127.0.0.1:") {
			t.Errorf("addr = %q，必须只绑回环", a)
		}
	}
}

// 停止后端口必须释放，否则反复启停会耗尽端口（Round 1 关口：异常退出清理）。
func TestStopReleasesFixedPortForReuse(t *testing.T) {
	emb, stop, err := serveOn(&stubApps{}, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := emb.addr
	stop()

	// 同一个地址应能立即重新绑定
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("停止后端口 %s 仍被占用: %v", addr, err)
	}
	ln.Close()
}

// WebView2 缺失提示必须是中文且带下载链接。
//
// 这些文案是「装不了软件」的用户唯一能看到的东西。留空会退回 Wails 的英文默认
// 文案，对国内用户等于没提示（D-010：检测并引导下载，不内置运行时）。
func TestWebView2MessagesAreChineseAndActionable(t *testing.T) {
	m := webviewMessages()

	cases := map[string]string{
		"InstallationRequired": m.InstallationRequired,
		"UpdateRequired":       m.UpdateRequired,
		"Webview2NotInstalled": m.Webview2NotInstalled,
		"InvalidFixedWebview2": m.InvalidFixedWebview2,
		"WebView2ProcessCrash": m.WebView2ProcessCrash,
		"FailedToInstall":      m.FailedToInstall,
		"MissingRequirements":  m.MissingRequirements,
	}
	for name, v := range cases {
		if strings.TrimSpace(v) == "" {
			t.Errorf("%s 为空：用户看到空白对话框，无从下手", name)
			continue
		}
		if !containsCJK(v) {
			t.Errorf("%s 不是中文：%q", name, v)
		}
	}

	// 关键的两条必须给出下载链接，否则用户无处可去。
	for _, name := range []string{"InstallationRequired", "FailedToInstall", "DownloadPage"} {
		v := cases[name]
		if name == "DownloadPage" {
			v = m.DownloadPage
		}
		if !strings.Contains(v, "developer.microsoft.com/microsoft-edge/webview2") {
			t.Errorf("%s 缺少 WebView2 下载链接：%q", name, v)
		}
	}

	// 不能残留 Wails 的英文默认文案
	if strings.Contains(m.InstallationRequired, "The WebView2 runtime is required") {
		t.Error("InstallationRequired 仍是 Wails 英文默认文案")
	}
}

func containsCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
