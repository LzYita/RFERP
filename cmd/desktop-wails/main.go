// 入口：Wails 桌面外壳 + 内嵌 HTTP 服务（阶段 A，Round 1 关口）。
//
// 形态：Wails 只负责窗口与 WebView2 运行时；所有业务与页面都由一个绑在
// 127.0.0.1 随机端口上的 HTTP 服务提供，与 cmd/server 共用同一套 api 与装配。
// 「桌面」和「服务器」不是两份实现，只是启动方式不同。
//
// 为什么不把 URL 直接交给 Wails：Wails v2 没有「让窗口导航到外部 URL」的选项，
// 窗口始终由 AssetServer 驱动。因此这里让 AssetServer 变成反向代理，
// 由代理补上启动 Cookie 转发给内嵌服务（见 internal/api/startup.go）。
// 副作用是 token 全程不进入浏览器，JS 读不到、也不落盘。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"app/internal/api"
	"app/internal/bootstrap"
	"app/internal/config"
	"app/internal/logging"
	"app/internal/paths"
	"app/internal/singleinstance"
	"app/internal/usecase"
	"app/internal/webassets"
	"app/internal/winappid"
	"app/internal/winmsg"
)

var version = "dev"

func main() {
	// 开发/验证用：只起内嵌 HTTP 服务，不开 Wails 窗口。
	//
	// Vite 开发期需要 API 监听在一个已知端口上（见 vite.config.ts 的
	// RFERP_DEV_PORT），无头模式就是为它准备的。
	//
	// 注意：此模式**不启用启动 token**——没有 WebView 代理去注入 Cookie，
	// 启用后连自己人都进不去。它只用于本机开发，不要用于交付。
	serveOnly := flag.Bool("serve", false, "只启动内嵌 HTTP 服务，不开窗口（开发/验证用）")
	serveAddr := flag.String("serve-addr", "127.0.0.1:54321", "-serve 模式的监听地址")
	flag.Parse()

	winappid.Set("LzYita.RFERP")

	// 更新后重启时旧进程可能仍在退出，稍等它释放单实例锁。
	var lockWait time.Duration
	if os.Getenv("RFERP_UPDATE_RESTART") == "1" {
		lockWait = 20 * time.Second
	}
	if ok, err := singleinstance.Acquire("RFERP.SingleInstance", lockWait); err == nil && !ok {
		winmsg.Info("RFERP", "RFERP 已在运行，请勿重复启动。")
		return
	}

	cfg := config.Load()
	paths.SetDataDir(cfg.DataDir)
	if err := logging.Init(filepath.Join(paths.DataDir(), "日志")); err != nil {
		log.Printf("init log file failed: %v", err)
	}

	res, err := bootstrap.Build(cfg, bootstrap.Options{Logf: log.Printf})
	if err != nil {
		// 无头模式下没有界面可以承载更详细的说明，直接退出即可；
		// 弹系统对话框在无 GUI 的开发环境里既看不见也挡不住 CI。
		if *serveOnly {
			log.Fatalf("bootstrap failed: %v", err)
		}
		// 阶段 A 的内嵌服务必须同时支持 SQLite 与 MySQL；两者都失败时
		// 没有界面可以承载更详细的说明，先落到系统对话框。
		winmsg.Error("RFERP 无法启动", err.Error())
		os.Exit(1)
	}
	defer func() {
		if cerr := res.Close(); cerr != nil {
			log.Printf("close database: %v", cerr)
		}
	}()

	if *serveOnly {
		runHeadless(res.Apps, *serveAddr)
		return
	}

	emb, stop, err := serve(res.Apps)
	if err != nil {
		winmsg.Error("RFERP 无法启动", err.Error())
		os.Exit(1)
	}
	defer stop()

	log.Printf("RFERP %s embedded server on http://%s (storage=%s)", version, emb.addr, res.Kind)

	winW, winH := fitWindow(1360, 860)
	if err := wails.Run(&options.App{
		Title:  "RFERP-仁风仓库管理系统 v" + version,
		Width:  winW,
		Height: winH,
		// Assets 留空：全部请求都交给 Handler（反向代理）。
		// 若在此挂载内嵌资源，GET 会先被静态文件命中，代理就收不到了。
		AssetServer: &assetserver.Options{Handler: emb.proxyHandler()},
		Windows: &windows.Options{
			WebviewGpuIsDisabled: false,
			// WebView2 缺失/版本异常/崩溃时的提示。
			//
			// 这些文案必须中文且可操作：出现这个页面的用户装不了软件，
			// 只有明确告诉他去哪里下载才可能自己解决。留空会退回 Wails 的
			// 英文默认文案，对国内用户等于没提示（D-010：检测并引导下载，不内置）。
			Messages: webviewMessages(),
		},
	}); err != nil {
		log.Printf("wails exited with error: %v", err)
		os.Exit(1)
	}
}

// runHeadless 只提供 HTTP 服务，供前端开发与自动化验证使用。
//
// 与窗口模式的差别只有一处：不启用启动 token（没有 WebView 代理注入 Cookie）。
// 其余——装配、静态资源、会话、鉴权——完全相同，因此用它验证界面是有意义的。
func runHeadless(apps usecase.Applications, addr string) {
	apiSrv := api.New(apps,
		api.WithVersion(version),
		api.WithStaticFS(webassets.FS(), webassets.Dir),
	)
	ln, err := listenLoopback(addr)
	if err != nil {
		log.Fatalf("%v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	apiSrv.StartSessionJanitor(ctx)

	log.Printf("RFERP %s headless on http://%s/  (Ctrl+C 停止)", version, ln.Addr())
	srv := &http.Server{
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	}()
	log.Fatal(srv.Serve(ln))
}

// embeddedServer 是已就绪的内嵌服务。
type embeddedServer struct {
	addr     string // host:port
	target   *url.URL
	token    string
	http     *http.Server
	cancel   context.CancelFunc
	listener net.Listener
}

// listenLoopback 在回环地址上监听。
//
// 窗口模式传 ":0" 交给内核分配，因此永远不会撞端口——这是选随机端口的原因。
// -serve 模式用固定端口（Vite 开发期需要一个已知地址），这时可能撞上，
// 所以错误信息必须说清「换一个端口」，而不是只抛一句 address already in use。
func listenLoopback(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	if isAddrInUse(err) {
		return nil, fmt.Errorf("端口 %s 已被占用：%w\n"+
			"请换一个端口，例如 -serve-addr 127.0.0.1:%d；"+
			"桌面窗口模式不受影响，它使用内核分配的随机端口",
			addr, err, randomPortSuggestion(addr))
	}
	return nil, fmt.Errorf("绑定回环地址 %s: %w", addr, err)
}

func isAddrInUse(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	// 10048 是 Windows 的 WSAEADDRINUSE；Go 的 syscall.EADDRINUSE 在 Windows 上
	// 是另一个值（536870914），直接 errors.Is 判定永远为假，会走进普通失败分支
	// 而丢掉「换个端口」的提示。非 Windows 平台用 EADDRINUSE 即可。
	return errno == syscall.Errno(10048) || errno == syscall.EADDRINUSE
}

// randomPortSuggestion 给一个大概率空闲的端口号，避免用户只能干瞪眼。
func randomPortSuggestion(addr string) int {
	for i := 0; i < 20; i++ {
		p := 54321 + i
		if c, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p)); err == nil {
			c.Close()
			return p
		}
	}
	return 54321
}

// serve 在 127.0.0.1 上启动内嵌服务。
func serve(apps usecase.Applications) (*embeddedServer, func(), error) {
	return serveOn(apps, "127.0.0.1:0")
}

// serveOn 在指定地址启动内嵌服务。addr 为 ":0" 时由内核分配端口
// （Round 1 关口：随机端口分配、就绪等待、异常退出清理）。
func serveOn(apps usecase.Applications, addr string) (*embeddedServer, func(), error) {
	tok, err := newStartupToken()
	if err != nil {
		return nil, nil, fmt.Errorf("生成启动 token: %w", err)
	}

	apiSrv := api.New(apps,
		api.WithVersion(version),
		api.WithStartupToken(tok),
		// 页面由内嵌服务提供：代理把 "/" 转发过来，这里返回前端构建产物。
		// 这样页面与 /api/ 同源，启动 Cookie 与 Bearer 都不跨源。
		api.WithStaticFS(webassets.FS(), webassets.Dir),
	)

	// 就绪等待：Listen 成功即代表端口已可用，之后才 Serve。
	ln, err := listenLoopback(addr)
	if err != nil {
		return nil, nil, err
	}

	httpSrv := &http.Server{
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	apiSrv.StartSessionJanitor(ctx)

	go func() {
		if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("embedded server stopped: %v", err)
		}
	}()

	stop := func() {
		cancel()
		// 先关监听器，再优雅关闭。
		//
		// 顺序不能反、也不能只靠 Shutdown：http.Server 只有在 Serve() 被调用后
		// 才会登记监听器。若在登记之前就 Shutdown，它看不到任何监听器，会立刻返回，
		// 而 Serve() 随后照常开始服务——端口永远不释放，反复启停会逐渐耗尽端口
		// （Round 1 关口：异常退出清理）。直接关 ln 不依赖那个时序。
		_ = ln.Close()
		shutdownCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		_ = httpSrv.Shutdown(shutdownCtx)
	}

	return &embeddedServer{
		addr:     ln.Addr().String(),
		target:   &url.URL{Scheme: "http", Host: ln.Addr().String()},
		token:    tok,
		http:     httpSrv,
		cancel:   cancel,
		listener: ln,
	}, stop, nil
}

// proxyHandler 返回把 WebView 请求转发到内嵌服务的处理器。
//
// 转发时补上启动 Cookie：只有经由本进程 AssetServer 的请求才会带上它，
// 因此同机其它进程直连该端口一律被内嵌服务拒绝。
func (e *embeddedServer) proxyHandler() http.Handler {
	rp := httputil.NewSingleHostReverseProxy(e.target)
	inner := rp.Director
	rp.Director = func(r *http.Request) {
		inner(r)
		r.Host = e.target.Host
		// 已带 Cookie 时不覆盖，避免被前端伪造的同名 Cookie 影响判断。
		if _, err := r.Cookie(api.StartupCookieName); err != nil {
			r.AddCookie(&http.Cookie{Name: api.StartupCookieName, Value: e.token})
		}
	}
	return rp
}

// webviewMessages 返回 WebView2 相关的中文提示。
//
// 出现这些页面的用户装不了软件，只有明确告诉他去哪里下载才可能自己解决。
// 留空会退回 Wails 的英文默认文案，对国内用户等于没提示。
// D-010：检测并引导下载，不内置运行时。
func webviewMessages() *windows.Messages {
	m := windows.DefaultMessages()
	m.InstallationRequired = "缺少 Microsoft Edge WebView2 运行时，RFERP 无法显示界面。\n\n" +
		"点击「确定」将自动下载并安装，装完后重新启动 RFERP 即可。\n" +
		"若自动安装失败，请手动下载：" +
		"https://developer.microsoft.com/microsoft-edge/webview2/"
	m.UpdateRequired = "Microsoft Edge WebView2 运行时版本过旧，需要更新后才能显示界面。\n\n" +
		"点击「确定」将自动下载并安装最新版本。"
	m.MissingRequirements = "缺少运行 RFERP 所需的组件"
	m.Webview2NotInstalled = "未检测到 Microsoft Edge WebView2 运行时"
	m.InvalidFixedWebview2 = "指定的 WebView2 运行时路径无效。\n\n" +
		"请改用系统已安装的 WebView2，或重新安装该运行时后重试。"
	m.WebView2ProcessCrash = "界面进程意外退出。\n\n" +
		"如果反复出现，请更新 WebView2 运行时或重启电脑后重试。"
	m.FailedToInstall = "WebView2 运行时自动安装失败。\n\n" +
		"请手动下载安装后再启动：" +
		"https://developer.microsoft.com/microsoft-edge/webview2/"
	m.DownloadPage = "https://developer.microsoft.com/microsoft-edge/webview2/"
	m.PressOKToInstall = "点击「确定」开始下载安装"
	m.Error = "错误"
	m.ContactAdmin = "如需协助，请联系系统管理员。"
	return m
}

// newStartupToken 生成 24 字节随机 token（192 位），同机其它进程猜不到。
func newStartupToken() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
