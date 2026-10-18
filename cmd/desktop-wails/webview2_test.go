//go:build windows

package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// WebView2 缺失时的「预检 → 自动安装 → 失败重试」这一支，文案与按钮语义。
//
// 为什么值得单独测：这一支只有在"机器上没有 WebView2"时才走，而本机与 CI
// 都装着 WebView2（155.x），**永远跑不到**。也就是说这些文案是全代码库里
// 最难靠运行验证的部分 —— 只能靠断言把它们的契约钉住。
func TestWebviewInstallFailedNoticeIsActionable(t *testing.T) {
	const notice = webviewInstallFailedNotice

	if !containsCJK(notice) {
		t.Error("失败提示不是中文")
	}

	// Message dBox 只给「是/否/取消」，所以三个按钮各自做什么必须写在正文里，
	// 否则用户不知道该点哪个。
	for _, want := range []string{"「是」", "「否」", "「取消」"} {
		if !strings.Contains(notice, want) {
			t.Errorf("失败提示没说明 %s 按钮的作用：%q", want, notice)
		}
	}

	// 原因分类（对应三种用户能采取的不同动作）：权限、网络、策略。
	for _, want := range []string{"管理员", "网络", "策略"} {
		if !strings.Contains(notice, want) {
			t.Errorf("失败提示没提到「%s」这类原因，用户无法判断该找谁：%q", want, notice)
		}
	}

	// 手动出路必须具体到"选哪个包"：微软那个页面上有 Evergreen / Fixed 多组按钮。
	if !strings.Contains(notice, "Evergreen") {
		t.Error("失败提示没说手动安装该选哪个包（Evergreen）")
	}
	if !strings.Contains(notice, "x64") {
		t.Error("失败提示没说是 x64（页面上还有 x86 / ARM64 可选）")
	}

	// 重试是有限次的：无限循环会让用户只能强杀进程。
	if maxWebView2Attempts < 2 {
		t.Errorf("重试上限 %d 太小，等于没有重试", maxWebView2Attempts)
	}
	if maxWebView2Attempts > 5 {
		t.Errorf("重试上限 %d 太大，用户会被同一个失败弹很多次", maxWebView2Attempts)
	}
}

// 首次询问的文案不能再说"装完请重启"：装好之后 wails.Run 紧接着就建窗口，
// 不需要重启。这句写错会让用户多关一次程序。
func TestWebviewInstallPromptDoesNotAskForRestart(t *testing.T) {
	m := webviewMessages()

	if strings.Contains(m.InstallationRequired, "重新启动") {
		t.Errorf("InstallationRequired 仍让用户重启，但装好后程序会继续启动：%q", m.InstallationRequired)
	}
	if strings.Contains(m.UpdateRequired, "重新启动") {
		t.Errorf("UpdateRequired 仍让用户重启：%q", m.UpdateRequired)
	}
	if !strings.Contains(m.InstallationRequired, "确定") {
		t.Error("InstallationRequired 没说「确定」就是开始安装")
	}
	// 自动安装失败时要指向下载页，这条兜底文本里必须带链接。
	if !strings.Contains(m.InstallationRequired, webviewDownloadPage) {
		t.Error("InstallationRequired 没给手动下载链接作为兜底")
	}
}

// 取消之后的手动出路：说明 + 确定打开下载页（MessageBox 里的网址点不动）。
func TestWebviewCancelNoticeTellsHowToOpenThePage(t *testing.T) {
	if !strings.Contains(webviewCancelNotice, "确定") {
		t.Error("取消后的提示没说「确定」会打开下载页")
	}
	if !strings.Contains(webviewCancelNotice, webviewDownloadPage) {
		t.Error("取消后的提示没有下载链接")
	}
	if !strings.Contains(webviewCancelNotice, "未安装") || !strings.Contains(webviewCancelNotice, "版本过旧") {
		t.Error("取消后的提示没有同时覆盖「未安装」与「版本过旧」")
	}
}

// 我们把版本判定交回 Wails，所以这里不该再出现"最低版本"这种东西。
//
// 留着一条反向断言：一旦有人重新引入一个必须与 Wails 保持同步的常量，
// 就等于把"只有对方知道"的数字抄进了本仓库 —— 这条会提醒他先读 webview2_windows.go
// 顶部的理由。
func TestNoHardcodedWebView2Minimum(t *testing.T) {
	src, err := os.ReadFile("webview2_windows.go")
	if err != nil {
		t.Fatalf("读取 webview2_windows.go: %v", err)
	}

	// 只看代码，不看注释：文件顶部会提到这个版本号，用来说明"我们为什么不抄它"，
	// 那是文档不是判据。截断点用 " //"（空格+双斜杠），这样不会误伤
	// "https://..." 这类字符串里的双斜杠。
	var code []string
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if i := strings.Index(line, " //"); i >= 0 {
			line = line[:i]
		}
		code = append(code, line)
	}
	body := strings.Join(code, "\n")

	for _, banned := range []string{
		"CompareBrowserVersions",
		"minimumWebView2",
		"94.0.992.31",
	} {
		if strings.Contains(body, banned) {
			t.Errorf("webview2_windows.go 的**代码**里又出现了 %q：版本判定应当由 Wails 负责，"+
				"抄一个它内部的常量只能靠测试提醒同步（理由见该文件顶部）", banned)
		}
	}
}

// 用户放弃时返回的是"安静退出"这个哨兵错误，调用方据此 os.Exit 而不打印错误。
func TestErrWebView2UnavailableIsSentinel(t *testing.T) {
	if errors.Is(errWebView2Unavailable, errWebView2Unavailable) != true {
		t.Fatal("errWebView2Unavailable 应当是可用 errors.Is 判断的哨兵")
	}
	if errWebView2Unavailable.Error() == "" {
		t.Error("哨兵错误没有文案，日志里会显示空行")
	}
}
