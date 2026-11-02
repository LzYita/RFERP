package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"app/internal/api"
	"app/internal/config"
	"app/internal/deviceid"
	"app/internal/webassets"
	"app/internal/winmsg"
)

// 客户端模式的启动路径（局域网版 FR-10/11/12/17）。
//
// 与单机形态的关键差别：**不打开任何本地业务库**。所有业务请求由内嵌服务
// 转成指向主机的 HTTP 调用（api.Client），本机只负责窗口与页面。
// 因此这里不 bootstrap、不迁移、不建备份——那些都是主机的责任。

// deviceIdentity 是本机上报给主机的设备事实。
//
// 指纹每次启动重算而不是缓存：缓存一份旧指纹会让"换了主板"永远登记不上，
// 而重算的代价是读一次注册表 + 一次卷信息查询，可以忽略。
func deviceIdentity() api.DeviceIdentity {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		name = "未命名设备"
	}
	return api.DeviceIdentity{
		ID:      deviceid.Fingerprint(),
		Name:    name,
		Version: version,
		OS:      "windows",
	}
}

// normalizeServerURL 把用户输入归一成 http://host:port。
//
// 客户现场最常见的输入是 "192.168.1.10" 或 "192.168.1.10:8080"，
// 让他们必须手打 http:// 是没必要的摩擦；反过来，协议写错（https）时
// 不能静默改成 http——那会让"我明明填了 https"的客户以为程序在骗他。
func normalizeServerURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("请填写主机地址，例如 192.168.1.10 或 http://192.168.1.10:8080")
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("地址格式不正确：%v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("仅支持 http:// 或 https://，当前是 %s://", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("地址里缺少主机名或 IP")
	}
	if u.Port() == "" {
		u.Host = u.Host + ":8080"
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return strings.TrimRight(u.String(), "/"), nil
}

// switchServer 校验并切换主机地址（界面在登录前调用）。
//
// 先探测再落盘：把"连不上"当场告诉用户，比让他回到登录页面对一个
// 永远转圈的按钮强得多。探测失败**不**回滚——用户可能是在改一个
// 现在确实连不上、但稍后会开机的地址。
func switchServer(cli *api.Client, cfg *config.Config, raw string) (string, error) {
	u, err := normalizeServerURL(raw)
	if err != nil {
		return "", err
	}
	probe := api.NewClient(u)
	probe.SetDevice(deviceIdentity())
	info, err := probe.Describe()
	if err != nil {
		return "", fmt.Errorf("连接 %s 失败：%v（请确认主机已启动、IP 正确、防火墙放行 %s）",
			u, err, portOf(u))
	}
	// 换地址时同样要过版本门禁：连上一台要求更高版本的主机，
	// 登进去只会得到一堆 426。
	if api.VersionLess(version, info.MinClientVersion) {
		return "", fmt.Errorf("主机要求客户端 %s 以上（当前 %s），请先升级客户端", info.MinClientVersion, version)
	}
	previousMode, previousURL := cfg.Mode, cfg.ServerURL
	if err := cfg.SetRunMode(config.ModeClient, u); err != nil {
		cfg.Mode, cfg.ServerURL = previousMode, previousURL
		return "", fmt.Errorf("保存主机地址失败: %w", err)
	}
	cli.SetServerURL(u)
	log.Printf("lan-client: 已切换到 %s", u)
	return u, nil
}

func portOf(u string) string {
	if pu, err := url.Parse(u); err == nil && pu.Port() != "" {
		return pu.Port()
	}
	return "8080"
}

// showFatal 在无窗口可承载错误的阶段给一个可读的出口。
func showFatal(title, msg string) {
	winmsg.Error(title, msg)
	os.Exit(1)
}

// runHeadlessClient 是客户端模式的 -serve：只起内嵌服务，供前端开发与自动化。
func runHeadlessClient(cli *api.Client, cfg *config.Config, addr, updateURL string) {
	apiSrv := api.New(cli,
		api.WithVersion(version),
		api.WithStaticFS(webassets.FS(), webassets.Dir),
		api.WithUpdate(updateURL, version),
		// 与窗口模式共用 switchServer：只归一化地址而不探测、不落盘，
		// 会让"改地址"在无界面模式下变成一个假动作（用户以为连上了）。
		api.WithLANConnect(func(raw string) (string, error) { return switchServer(cli, cfg, raw) }),
	)
	ln, err := listenLoopback(addr)
	if err != nil {
		log.Fatalf("%v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	apiSrv.StartSessionJanitor(ctx)
	log.Printf("RFERP %s 客户端无界面模式 → %s (Ctrl+C 停止)", version, cli.ServerURL())
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

// runLANClient 启动客户端模式窗口。
//
// serveOnly（-serve）在这里同样有效：它只起内嵌 HTTP 服务，供前端开发与
// 自动化验证。此时不弹任何对话框。
func runLANClient(cfg *config.Config, updateURL string, serveOnly bool, serveAddr string) {
	base, addressErr := normalizeServerURL(cfg.ServerURL)
	if addressErr != nil {
		// 配置里写了 mode=client 却没有地址：这不是"未配置"，而是配置损坏。
		// 回到首启向导会让用户以为要重新配库，所以直接提示并退出。
		log.Printf("lan-client: 主机地址无效: %v", addressErr)
		if !serveOnly {
			showFatal("RFERP 需要主机地址", addressErr.Error()+"\n\n请在 config.json 的 serverUrl 里填写主机 IP。")
		}
		return
	}

	cli := api.NewClient(base)
	cli.SetDevice(deviceIdentity())

	// 启动时探一次：连不上不阻塞启动（主机可能稍后才开机），
	// 但必须在日志里留下一行明确的地址与失败原因，否则远程排障无从下手。
	if info, err := cli.Describe(); err != nil {
		log.Printf("lan-client: 预检 %s 失败（界面仍会打开，登录时再试）: %v", base, err)
	} else {
		log.Printf("lan-client: 已连接 %s（服务器版本 %s，schema %d，最低客户端 %q）",
			base, info.AppVersion, info.SchemaVersion, info.MinClientVersion)
		// 版本不兼容是**启动前就能判定**的：直接带说明退出，比让用户
		// 面对一个每个操作都 426 的界面好（FR-15 / R12）。
		if api.VersionLess(version, info.MinClientVersion) {
			log.Printf("lan-client: 客户端版本过低（%s < %s）", version, info.MinClientVersion)
			if !serveOnly {
				showFatal("客户端版本过低",
					"这台主机要求客户端版本 "+info.MinClientVersion+" 或更高（当前 "+version+"）。请安装新版本客户端后再连接。")
				return
			}
		}
	}

	if serveOnly {
		runHeadlessClient(cli, cfg, serveAddr, updateURL)
		return
	}

	dirPicker := newNativeDirPicker()
	emb, stop, err := serveOnClient(cli, "127.0.0.1:0", updateURL, dirPicker, func(raw string) (string, error) {
		return switchServer(cli, cfg, raw)
	})
	if err != nil {
		showFatal("RFERP 无法启动", err.Error())
		return
	}
	defer stop()

	log.Printf("RFERP %s 客户端模式 → %s（本机界面 http://%s）", version, base, emb.addr)
	runDesktopWindow(emb, dirPicker)
}
