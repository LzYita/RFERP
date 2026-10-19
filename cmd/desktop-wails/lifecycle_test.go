package main

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestListenLoopbackRejectsNonLoopback(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:0", ":0", "[::]:0", "192.0.2.1:0", "[2001:db8::1]:0", "example.invalid:0", "invalid"} {
		t.Run(addr, func(t *testing.T) {
			ln, err := listenLoopback(addr)
			if ln != nil {
				ln.Close()
				t.Fatal("non-loopback listener opened")
			}
			if err == nil || !strings.Contains(err.Error(), "只允许回环地址") {
				t.Fatalf("expected explicit loopback rejection, got %v", err)
			}
		})
	}
}

func TestListenLoopbackAcceptsLocalAddresses(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0", "[::ffff:127.0.0.1]:0"} {
		t.Run(addr, func(t *testing.T) {
			ln, err := listenLoopback(addr)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			bound := ln.Addr().(*net.TCPAddr)
			if !bound.IP.IsLoopback() || bound.Port == 0 {
				t.Fatalf("expected bound loopback address, got %s", bound)
			}
			conn, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatalf("loopback listener unreachable: %v", err)
			}
			conn.Close()
		})
	}
}

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

	_, _, err = serveOn(&stubApps{}, addr, "")
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
		emb, stop, err := serveOn(&stubApps{}, "127.0.0.1:0", "")
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
	emb, stop, err := serveOn(&stubApps{}, "127.0.0.1:0", "")
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

// 「点取消」之后必须再给一次中文说明。
//
// 背景（2026-10-18）：Wails 把用户取消当成普通 error 返回
// （internal/wv2installer/download.go 里 errors.New(messages.Webview2NotInstalled)），
// 而我们原先把任何 wails.Run 错误都只写日志就 os.Exit(1)。结果是：用户点了取消，
// 程序直接消失，屏幕上什么都没留下 —— 连"该去哪儿装"都没说。
//
// 这条测试守两件事：识别函数认得出那个错误（不认其它错误），以及那段文案本身可操作。
func TestWebviewCancellationTellsTheUserWhatToDo(t *testing.T) {
	m := webviewMessages()

	// 识别：Wails 返回的正是这个字符串，所以按字符串比对是唯一可行的判据。
	if !isWebview2Unavailable(errors.New(m.Webview2NotInstalled)) {
		t.Fatal("认不出 Wails 在用户取消时返回的 Webview2NotInstalled")
	}

	// 不能把别的错误误判成 WebView2 缺失 —— 那会让正常启动失败也弹这条提示并退出。
	for _, other := range []string{
		"",
		"some unrelated failure",
		m.FailedToInstall,
		m.InvalidFixedWebview2,
	} {
		if other == "" {
			if isWebview2Unavailable(nil) {
				t.Error("nil 错误被判成 WebView2 缺失")
			}
			continue
		}
		if isWebview2Unavailable(errors.New(other)) {
			t.Errorf("%q 被误判成 WebView2 缺失", other)
		}
	}

	// 文案本身：中文、有下载链接、并且同时覆盖「没装」与「版本过旧」——
	// Wails 对这两种情况返回的是同一个错误，只写"未安装"会误导。
	if !containsCJK(webviewCancelNotice) {
		t.Error("取消后的提示不是中文")
	}
	if !strings.Contains(webviewCancelNotice, "developer.microsoft.com/microsoft-edge/webview2") {
		t.Errorf("取消后的提示没有下载链接，用户无处可去：%q", webviewCancelNotice)
	}
	if !strings.Contains(webviewCancelNotice, "未安装") {
		t.Error("取消后的提示没说明「未安装」这种可能")
	}
	if !strings.Contains(webviewCancelNotice, "版本过旧") {
		t.Error("取消后的提示没说明「版本过旧」这种可能，而 Wails 对两者返回同一个错误")
	}

	// 必须说清「确定」会做什么。MessageBox 里的网址是点不动的，如果文案只写
	// "请手动下载"，用户会以为那个确定按钮只是关闭，然后自己去猜怎么下载。
	if !strings.Contains(webviewCancelNotice, "确定") {
		t.Error("取消后的提示没说明「确定」按钮的作用（应当用它打开下载页）")
	}

	// 动作与文案必须指向同一个地址，否则用户看到的和浏览器打开的不是同一页。
	if m.DownloadPage != webviewDownloadPage {
		t.Errorf("文案里的下载页 %q 与 OpenURL 使用的 %q 不一致", m.DownloadPage, webviewDownloadPage)
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
