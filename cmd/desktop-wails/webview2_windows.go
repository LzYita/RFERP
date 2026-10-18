//go:build windows

package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
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
//
// **只判"有没有"，不判"够不够新"。**
// 版本下限是 Wails 编译时所用 WebView2 SDK 的能力约束（它内部写作
// MinimumRuntimeVersion = 94.0.992.31），而那个常量在 Wails 的 internal 包里，
// 我们 import 不到。抄一个只有对方知道的数字，只能靠测试提醒同步，且一旦抄错就会
// 出现"我们放行、Wails 再拦"或者反过来的多余提示。所以版本判定完全交回 Wails：
// 它已经实现了这个检查，用的也是我们设的中文文案。
//
// 代价是明确的：**版本过旧**这条分支仍走 Wails 的流程，没有重试、没有失败分类。
// 只影响"装着旧版 WebView2"的机器（Edge 会自动更新它），而"完全没装"这条
// —— 干净 Win10、以及从旧版升级来的机器 —— 由我们完整接管。

// webview2BootstrapperURL 是微软官方的 Evergreen 引导程序（x64），
// 与 Wails 内部用的是同一个 fwlink（internal/webview2runtime/download.go:47）。
const webview2BootstrapperURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

// maxWebView2Attempts 是自动安装的重试上限。
// 不设上限的话，"装完仍检测不到"会变成无限循环，用户只能强杀进程。
const maxWebView2Attempts = 3

// detectWebView2 报告系统里有没有可用的 WebView2，以及它的版本号。
//
// 只区分"有"与"没有"：版本够不够新由 Wails 判断，理由见文件头。
// 查询本身出错时按"没有"处理 —— 用户要做的事一样，而且拿不到版本号。
func detectWebView2() (version string, present bool) {
	version, err := webviewloader.GetAvailableCoreWebView2BrowserVersionString("")
	if err != nil {
		log.Printf("webview2: 查询版本失败: %v", err)
		return "", false
	}
	if version == "" {
		return "", false
	}
	return version, true
}

// ensureWebView2 保证 WebView2 已安装；装不上且用户放弃时返回
// errWebView2Unavailable。所有面向用户的说明都在这里说完。
func ensureWebView2() error {
	if version, present := detectWebView2(); present {
		log.Printf("webview2: 已安装，版本 %s（是否满足要求由 Wails 判断）", version)
		return nil
	}

	// 第一次：征求安装许可。取消 = 走手动出路。
	if !askInstall() {
		return offerManualInstall()
	}

	for attempt := 1; attempt <= maxWebView2Attempts; attempt++ {
		installErr := installWebView2()
		if installErr == nil {
			if version, present := detectWebView2(); present {
				// 装好即可继续，不需要用户重启：wails.Run 紧接着就会创建窗口。
				log.Printf("webview2: 安装成功，版本 %s", version)
				return nil
			}
			// 安装程序说成功了，但系统里查不到 —— 说明装到了别处或被策略拦住，
			// 不能对用户说"已装好"。
			installErr = fmt.Errorf("安装程序已结束，但仍检测不到 WebView2")
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

// askInstall 询问是否现在自动安装。标题与正文用与 Wails 相同的字段，
// 这样两条路的文案一致。
func askInstall() bool {
	m := webviewMessages()
	return winmsg.ConfirmOK(m.MissingRequirements, m.InstallationRequired)
}

// installWebView2 下载官方引导程序并静默运行。
//
// 不主动提权：与 Wails 一致，由引导程序自己的 manifest 触发 UAC。
// 用户拒绝 UAC 时安装程序以非零码退出，这里如实转成错误。
func installWebView2() error {
	// 随机文件名而不是固定的 MicrosoftEdgeWebview2Setup.exe：
	// 固定名会被两个同时更新的实例互相覆盖，也让同用户的其它进程可以预占。
	f, err := os.CreateTemp("", "MicrosoftEdgeWebview2Setup-*.exe")
	if err != nil {
		return fmt.Errorf("无法创建临时文件：%w", err)
	}
	installer := f.Name()
	f.Close()

	if err := downloadFile(webview2BootstrapperURL, installer); err != nil {
		os.Remove(installer)
		return fmt.Errorf("下载安装程序失败（多为网络受限或代理拦截）：%w", err)
	}
	defer os.Remove(installer)

	// 执行前验签。失败就不执行 —— 宁可让用户走手动下载页，也不运行一个
	// 来路不明的二进制（它紧接着还会请求管理员提权）。
	if err := verifyAuthenticode(installer); err != nil {
		return fmt.Errorf("下载的安装程序未通过签名校验，已拒绝执行（请手动到官网安装）：%w", err)
	}

	cmd := exec.Command(installer)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("无法启动安装程序：%w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("安装程序未正常结束（多为 UAC 提权被拒或企业策略拦截）：%w", err)
	}
	return nil
}

// maxBootstrapperBytes 是下载上限。安装程序约 2 MB；200 MB 留足余量，
// 同时挡住"异常服务器在 60 秒超时内灌几百 MB"这种消耗。
const maxBootstrapperBytes = 200 << 20

// httpsOnlyRedirect 保证重定向不降级。
//
// Go 的 http.Client 默认**不限制**重定向协议，https → http 会照跟 ——
// 而下载回来的文件我们随后要执行。这里只放行 https，并限制跳转次数。
func httpsOnlyRedirect(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("拒绝重定向到非 https 地址（%s）", req.URL.Scheme)
	}
	if len(via) >= 5 {
		return fmt.Errorf("重定向次数过多（%d 次）", len(via))
	}
	return nil
}

// downloadFile 把 url 落到 dst。超时是必要的：fwlink 会重定向，
// 在没有网络的环境里不设超时会挂到天荒地老。
func downloadFile(url, dst string) error {
	client := &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: httpsOnlyRedirect,
	}
	return downloadTo(client, url, dst, maxBootstrapperBytes)
}

// downloadTo 是 downloadFile 的可测内核（limit 可注入，测试不必造 200 MB）。
func downloadTo(client *http.Client, url, dst string, limit int64) error {
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
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("响应体超过上限 %d MB，已中止", limit>>20)
	}
	return nil
}
