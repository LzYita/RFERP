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
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
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

	emb, stop, err := serve(res.Apps)
	if err != nil {
		winmsg.Error("RFERP 无法启动", err.Error())
		os.Exit(1)
	}
	defer stop()

	log.Printf("RFERP %s embedded server on http://%s (storage=%s)", version, emb.addr, res.Kind)

	if err := wails.Run(&options.App{
		Title:  "RFERP-仁风仓库管理系统 v" + version,
		Width:  1360,
		Height: 860,
		// Assets 留空：全部请求都交给 Handler（反向代理）。
		// 若在此挂载内嵌资源，GET 会先被静态文件命中，代理就收不到了。
		AssetServer: &assetserver.Options{Handler: emb.proxyHandler()},
		Windows: &windows.Options{
			WebviewGpuIsDisabled: false,
		},
	}); err != nil {
		log.Printf("wails exited with error: %v", err)
		os.Exit(1)
	}
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

// serve 在 127.0.0.1 的随机端口上启动内嵌服务。
//
// 端口用 :0 交给内核分配，避免与本机已有服务冲突（Round 1 关口：随机端口分配、
// 就绪等待、异常退出清理）。只绑回环，不监听 0.0.0.0。
func serve(apps usecase.Applications) (*embeddedServer, func(), error) {
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

	// 就绪等待：先 Listen 拿到端口即代表端口可用，再开始 Serve。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("绑定回环端口: %w", err)
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

// newStartupToken 生成 24 字节随机 token（192 位），同机其它进程猜不到。
func newStartupToken() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
