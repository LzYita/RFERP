package ui

import (
	"fmt"
	"log"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/help"
	"app/internal/update"
)

// updateProbe 抽象「查询最新版本」，便于测试替换掉真实网络请求。
type updateProbe interface {
	// Probe 返回线上最新版本号。若已是最新，latest 为空且 err 为 nil。
	Probe() (latest string, err error)
}

// manifestProbe 通过既有的（带签名校验的）更新清单查询最新版本。
//
// 复用 update.Checker 而不是自己发 HTTP 请求，是为了沿用同一套验签逻辑：
// 界面上「已是最新」的结论不应该建立在未经验证的数据上。
type manifestProbe struct {
	checker *update.Checker
}

func (p *manifestProbe) Probe() (string, error) {
	m, err := p.checker.Check()
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", nil // 已是最新
	}
	return m.Version, nil
}

// newUpdateProbe 构造查询器；updateURL 为空时返回 nil。
func newUpdateProbe(updateURL, version string) updateProbe {
	if updateURL == "" {
		return nil
	}
	c, err := update.NewChecker(updateURL, version)
	if err != nil {
		log.Printf("settings: update checker unavailable: %v", err)
		return nil
	}
	return &manifestProbe{checker: c}
}

// guidePage 是「使用指南」页：顶部版本区 + 正文。
type guidePage struct {
	versionLabel *widget.Label
	latestLabel  *widget.Label
	checkBtn     *widget.Button
	statusLabel  *widget.Label
	body         *widget.RichText
	probe        updateProbe
	version      string
}

func newGuidePage(version, updateURL string) fyne.CanvasObject {
	g := &guidePage{version: version}

	g.versionLabel = widget.NewLabelWithStyle("当前版本 v"+version, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	latestText := "最新版本：未知"
	if v := help.LatestVersion(); v != "" {
		latestText = "内置记录最新 v" + v
	}
	g.latestLabel = widget.NewLabel(latestText)

	g.statusLabel = widget.NewLabel("")
	g.statusLabel.Wrapping = fyne.TextWrapWord

	g.probe = newUpdateProbe(updateURL, version)
	g.checkBtn = widget.NewButtonWithIcon("查询最新更新", theme.ViewRefreshIcon(), g.runCheck)
	if g.probe == nil {
		g.checkBtn.Disable()
		g.setStatus("未配置更新地址，无法在线查询。")
	}

	// 用 Fyne 自带的 markdown 渲染（底层 goldmark），不自己写解析器。
	//
	// 指南刻意不使用代码块与引用块：应用只设置了 FYNE_FONT（黑体，含中文），
	// 没设置 FYNE_FONT_MONOSPACE，等宽样式会退回 Fyne 内置的 Noto Mono——
	// 那是纯拉丁字体，代码块里的中文会渲染成豆腐块 ◇；
	// 引用块还会因缩进在窄面板里溢出。internal/help 里有测试守住这条约束。
	// Markdown 表格同样不用——Fyne 的渲染器不支持表格。
	g.body = widget.NewRichTextFromMarkdown(help.Guide())
	g.body.Wrapping = fyne.TextWrapWord

	header := container.NewVBox(
		widget.NewLabelWithStyle("软件使用指南", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewHBox(g.versionLabel, g.latestLabel),
		container.NewHBox(g.checkBtn),
		g.statusLabel,
		widget.NewSeparator(),
	)
	return container.NewBorder(header, nil, nil, nil,
		container.NewVScroll(container.NewPadded(g.body)))
}

// runCheck 手动查询最新版本。下载与安装仍走既有的更新流程，不在这里重复实现。
func (g *guidePage) runCheck() {
	if g.probe == nil {
		g.setStatus("未配置更新地址，无法在线查询。")
		return
	}
	g.checkBtn.Disable()
	g.setStatus("正在查询最新版本…")
	runInBackground(func() {
		latest, err := g.probe.Probe()
		// Fyne 控件只能在主线程改动
		runOnMain(func() { g.applyProbeResult(latest, err) })
	})
}

// applyProbeResult 把查询结果写进状态区。抽出来是为了让测试能确定性地验证
// 三种结果分支，不必依赖 goroutine 时序。
func (g *guidePage) applyProbeResult(latest string, err error) {
	g.checkBtn.Enable()
	switch {
	case err != nil:
		g.setStatus("查询失败：" + err.Error())
	case latest == "":
		g.setStatus(fmt.Sprintf("当前 v%s 已是最新版本。", g.version))
	default:
		g.setStatus(fmt.Sprintf("发现新版本 v%s（当前 v%s）。可在启动提示或再次点击「查询最新更新」中安装。",
			latest, g.version))
	}
}

func (g *guidePage) setStatus(s string) {
	if g.statusLabel != nil {
		g.statusLabel.SetText(s)
	}
}

// runOnMain 在主线程执行 fn。
//
// 测试里会被替换成同步执行，避免依赖事件循环时序。
var runOnMain = func(fn func()) { fn() }

// runInBackground 在后台执行 fn（网络请求不能阻塞界面）。
//
// 同样做成可替换的变量：测试改为同步执行，才能确定性地断言结果。
var runInBackground = func(fn func()) { go fn() }
