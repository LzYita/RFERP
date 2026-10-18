//go:build windows

package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wailsapp/go-webview2/webviewloader"

	"app/internal/winmsg"
)

// 为什么自己在 wails.Run 之前做一遍 WebView2 预检
//
// Wails 自带 preflight，但那条路满足不了我们要的三件事：
//
//  1. **失败不可靠**：安装程序退出码非零时，它把弹框函数的返回值当成错误返回
//     （internal/wv2installer/download.go:31），而那个值正常是 nil —— 于是它
//     当作安装成功继续往下跑，最后报一个与 WebView2 无关的错误。
//  2. **没有重试**：装失败只能关掉应用重开，再从头点一遍。
//  3. **取消与失败同一处理**：两者都变成 Webview2NotInstalled 一个错误，
//     给不出不同的下一步。
//
// 自己检测 + 自己装，就能在同一个进程里重试，并按真实原因给建议。
// 我们的流程跑完再进 wails.Run，Wails 自己那次 preflight 会因为已经装好而直接通过。

// minimumWebView2 与 Wails 的 wv2installer.MinimumRuntimeVersion 保持一致。
// 不一致的后果是「我们放行、Wails 再拦一次」，同一件事弹两遍。
const minimumWebView2 = "94.0.992.31"

// webview2BootstrapperURL 是微软官方的 Evergreen 引导程序（x64），
// 与 Wails 内部用的是同一个 fwlink（internal/webview2runtime/download.go:47）。
const webview2BootstrapperURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

// maxWebView2Attempts 是自动安装的重试上限。
// 不设上限的话，"装完仍检测不到"会变成无限循环，用户只能强杀进程。
const maxWebView2Attempts = 3

type webview2State int

const (
	webview2Ready webview2State = iota
	webview2Missing
	webview2Outdated
)

// detectWebView2 报告系统里 WebView2 的状态与版本。
//
// 与 Wails 内部同一个函数、同一套比较：空版本号 = 没装，
// 版本低于 minimumWebView2 = 过旧。
func detectWebView2() (webview2State, string) {
	version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil {
		// 查询本身出错（不是"没装"）时按缺失处理：用户要做的事是一样的，
		// 而且没有版本号可用。
		log.Printf("webview2: 查询版本失败: %v", err)
		return webview2Missing, ""
	}
	if version == "" {
		return webview2Missing, ""
	}
	cmp, err := webviewloader.CompareBrowserVersions(version, minimumWebView2)
	if err != nil {
		log.Printf("webview2: 版本比较失败（%q）: %v", version, err)
		return webview2Missing, version
	}
	if cmp < 0 {
		return webview2Outdated, version
	}
	return webview2Ready, version
}

// ensureWebView2 保证 WebView2 可用；不可用且用户放弃时返回
// errWebView2Unavailable。所有面向用户的说明都在这里说完。
func ensureWebView2() error {
	state, version := detectWebView2()
	if state == webview2Ready {
		log.Printf("webview2: 版本 %s，满足最低要求 %s", version, minimumWebView2)
		return nil
	}

	// 第一次：征求安装许可。取消 = 走手动出路。
	if !askInstall(state, version) {
		return offerManualInstall()
	}

	for attempt := 1; attempt <= maxWebView2Attempts; attempt++ {
		installErr := installWebView2()
		if installErr == nil {
			if state, version = detectWebView2(); state == webview2Ready {
				// 装好即可继续，不需要用户重启：wails.Run 紧接着就会创建窗口。
				log.Printf("webview2: 安装成功，版本 %s", version)
				return nil
			}
			// 安装程序说成功了，但系统里查不到新版本 —— 这确实发生过的话，
			// 说明装到了别处或被策略拦住，不能对用户说"已装好"。
			installErr = fmt.Errorf("安装程序已结束，但检测到的版本仍不可用")
		}
		log.Printf("webview2: 第 %d/%d 次自动安装未成功: %v", attempt, maxWebView2Attempts, installErr)

		if attempt == maxWebView2Attempts {
			break
		}
		retry, openPage := winmsg.AskRetryOpenPage(
			webviewMessages().MissingRequirements, webviewInstallFailedNotice)
		if retry {
			continue
		}
		if openPage {
			return openDownloadPage()
		}
		return errWebView2Unavailable
	}

	// 次数用完：说清楚并给一次手动出路，不再循环。
	winmsg.Info(webviewMessages().MissingRequirements,
		fmt.Sprintf("已自动尝试安装 %d 次，仍未成功。\n\n%s", maxWebView2Attempts, webviewInstallFailedNotice))
	return offerManualInstall()
}

// askInstall 询问是否现在自动安装。title/body 用与 Wails 相同的字段，
// 这样两条路（我们的预检、Wails 自己的）文案一致。
func askInstall(state webview2State, version string) bool {
	m := webviewMessages()
	body := m.InstallationRequired
	if state == webview2Outdated {
		body = m.UpdateRequired
		if version != "" {
			body = fmt.Sprintf("Microsoft Edge WebView2 运行时版本过旧（当前 %s），需要更新后才能显示界面。\n\n", version) +
				"点「确定」将自动下载并安装最新版本；装好后程序会继续启动，不需要重启。\n" +
				"若自动安装失败，可手动下载：" + webviewDownloadPage
		}
	}
	return winmsg.ConfirmOK(m.MissingRequirements, body)
}

// installWebView2 下载官方引导程序并静默运行。
//
// 不主动提权：与 Wails 一致，由引导程序自己的 manifest 触发 UAC。
// 用户拒绝 UAC 时安装程序以非零码退出，这里如实转成错误。
func installWebView2() error {
	installer := filepath.Join(os.TempDir(), "MicrosoftEdgeWebview2Setup.exe")
	if err := downloadFile(webview2BootstrapperURL, installer); err != nil {
		return fmt.Errorf("下载安装程序失败（多为网络受限或代理拦截）：%w", err)
	}
	defer os.Remove(installer)

	cmd := exec.Command(installer)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法启动安装程序：%w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("安装程序未正常结束（多为 UAC 提权被拒或企业策略拦截）：%w", err)
	}
	return nil
}

// downloadFile 把 url 落到 dst。超时是必要的：fwlink 会重定向，
// 在没有网络的环境里不设超时会挂到天荒地老。
func downloadFile(url, dst string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return err
	}
	return nil
}
