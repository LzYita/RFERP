// 入口：把 Service 装配到 HTTP API（C1）。多人形态下仅服务端连数据库（D-013）。
//
// 装配逻辑在 internal/bootstrap，与 cmd/desktop-wails 共用同一份实现；
// 本入口只负责：配置来源、单实例、监听地址、日志、局域网准入与进程生命周期。
//
// 局域网版（lan-v1）在这里加的几件事，都是为了"无人值守的常驻服务"：
//   - 单实例锁：两个进程同时开同一个 SQLite 只会互相拆 WAL（R11）；
//   - 服务端选库只看 storage，不看 mode（R10，见 bootstrap.BuildServer）；
//   - 启动打印全部 LAN IPv4：客户现场没有远程支持，日志是唯一线索（R29）；
//   - /readyz：安装器与服务监控要判断"能不能干活"，而不只是"进程在不在"；
//   - 首建管理员令牌：空库那几分钟整个局域网都能连上来（FR-41）。
//
// 进程生命周期交给 internal/svcrun：同一个入口既能被 SCM 当服务启动，也能
// 在命令行里当控制台程序跑，交付包因此不需要 WinSW 这类第三方服务包装器。
package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"app/internal/api"
	"app/internal/bootstrap"
	"app/internal/config"
	"app/internal/logging"
	"app/internal/netinfo"
	"app/internal/paths"
	"app/internal/service"
	"app/internal/singleinstance"
	"app/internal/svcrun"
	"app/internal/usecase"
)

var version = "dev"

// logRetention 是服务端日志的保留期（D-25）。
const logRetention = 180 * 24 * time.Hour

// serviceName 必须与安装脚本注册的服务名一致。
//
// 两处都依赖它：svc.Run 用它与 SCM 里的注册名匹配（不一致会报 1083
// "可执行程序未实现该服务"），事件日志也以它作为来源名。
const serviceName = "RFERPServer"

func main() {
	configPath := flag.String("config", "", "配置文件路径；留空依次查 RFERP_CONFIG、exe 目录、当前目录、用户配置目录")
	addr := flag.String("addr", "", "HTTP 监听地址；留空用配置里的 server.addr，默认 :8080")
	showVersion := flag.Bool("version", false, "打印版本后退出")
	healthcheck := flag.Bool("healthcheck", false, "探测本机 /readyz 后退出（0=就绪，1=未就绪）")
	licenseFile := flag.String("license", "", "导入授权文件后退出")
	devicesCmd := flag.String("devices", "", "设备运维：list | revoke <设备指纹前12位> | delete <设备指纹前12位>")
	bootstrapToken := flag.String("bootstrap-token", "", "指定首建管理员令牌；留空自动生成并写入数据目录")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	cfg, err := config.LoadAt(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败：%v", err)
	}
	if *configPath != "" {
		log.Printf("config: 使用显式配置文件 %s", *configPath)
	}
	paths.SetDataDir(cfg.DataDir)
	logDir := filepath.Join(paths.DataDir(), "日志")
	// 服务端日志参数与桌面端不同：20MB × 20 份，再叠加 180 天年龄清理。
	// 两者必须同时给：只按份数会在第 3 天就滚掉，只按天数会让磁盘无上限。
	if err := logging.InitWith(logDir, logging.Options{MaxSize: 20 << 20, Keep: 20}); err != nil {
		log.Printf("init log file failed: %v", err)
	}
	if n, err := logging.PruneOlderThan(logDir, logRetention); err == nil && n > 0 {
		log.Printf("logging: 清理 %d 个超过 %d 天的日志文件", n, int(logRetention.Hours()/24))
	}

	if *healthcheck {
		os.Exit(runHealthcheck(*addr, cfg))
	}

	// CLI 动作是一次性运维命令（导入授权、查/撤设备），只在控制台里有意义。
	// 必须在服务判定之前分流：它们不该被 SCM 当服务主体启动。
	if isCLIAction(*licenseFile, *devicesCmd) {
		if err := runCLIAction(cfg, *licenseFile, *devicesCmd); err != nil {
			log.Fatalf("%v", err)
		}
		return
	}

	// 单实例：同一个 SQLite 文件被两个进程打开会互相拆 WAL/SHM（R11）。
	if ok, err := singleinstance.Acquire("RFERP.Server.SingleInstance", 0); err == nil && !ok {
		log.Fatal("已有一个 RFERP-server 在运行（单实例锁被占用），本次启动已退出")
	}

	listen := *addr
	if listen == "" {
		listen = cfg.Srv.Addr
	}

	// 启动主体可能很慢（连库、迁移、升级前备份），所以它必须在服务已向 SCM
	// 报到之后才执行——见 internal/svcrun 包注释里的 1053 说明。
	if err := svcrun.Run(serviceName, func(ctx context.Context, markReady func()) error {
		return serve(ctx, markReady, cfg, listen, logDir, *bootstrapToken)
	}); err != nil {
		log.Fatalf("RFERP-server 退出：%v", err)
	}
}

// serve 是装配 + 监听的完整主体，控制台模式与服务模式共用。
//
// markReady 必须在**端口真正绑定成功之后**调用：/readyz 回答的是"能不能干活"，
// 端口没绑上就报就绪，会让安装脚本在服务还接不了请求时就继续往下走。
func serve(ctx context.Context, markReady func(), cfg *config.Config, listen, logDir, explicitToken string) error {
	res, err := bootstrap.BuildServer(cfg, bootstrap.Options{Logf: log.Printf})
	if err != nil {
		if bootstrap.IsNotConfigured(err) {
			return fmt.Errorf("server 尚未配置数据库：%w", err)
		}
		return fmt.Errorf("bootstrap failed: %w", err)
	}
	defer func() {
		if cerr := res.Close(); cerr != nil {
			log.Printf("close database: %v", cerr)
		}
	}()

	svc, _ := res.Apps.(*service.Service)

	// 主机登记：不占授权额度，且是唯一能在授权锁死时救场的设备（R7）。
	hostID := ""
	if svc != nil {
		id, err := svc.EnsureHostDevice()
		if err != nil {
			log.Printf("lan: 登记主机设备失败（不影响启动，但主机可能被当成新客户端）: %v", err)
		}
		hostID = id
	}

	tok := bootstrapTokenForStartup(explicitToken, res.Apps)

	srv := api.New(res.Apps, api.WithVersion(version), api.WithReadiness(readiness(res)),
		api.WithDeviceGuard(deviceGuardOf(res.Apps)), api.WithBootstrapToken(tok),
		api.WithMinClientVersion(cfg.MinClientVersion))

	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	srv.StartSessionJanitor(ctx)
	go pruneLogsLoop(ctx, logDir)

	// 自动备份：服务端是常驻进程，必须靠定时器而不是"下次打开软件"。
	if sched, ok := res.Apps.(usecase.AutoBackupScheduler); ok {
		go sched.StartAutoBackupTicker(ctx.Done())
	}

	log.Printf("RFERP-server %s listening on %s (storage=%s target=%s config=%s)",
		version, listen, res.Kind, res.Target, cfg.Path())
	logStartupAddresses(listen, hostID)

	// 自己 Listen 而不是用 ListenAndServe：只有 bind 成功才谈得上"就绪"。
	// 端口被占用是局域网版最常见的启动失败原因，这里要能明确报出来。
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("监听 %s 失败（端口可能被占用）：%w", listen, err)
	}

	markReady()

	serveErr := make(chan error, 1)
	go func() {
		err := httpSrv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serveErr <- err
	}()

	select {
	case <-ctx.Done():
		log.Println("shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("graceful shutdown: %v", err)
		}
		return nil
	case err := <-serveErr:
		return err
	}
}

// pruneLogsLoop 每天清理一次过期日志，直到 ctx 结束。
func pruneLogsLoop(ctx context.Context, logDir string) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := logging.PruneOlderThan(logDir, logRetention); err != nil {
				log.Printf("logging: 清理旧日志失败: %v", err)
			}
		}
	}
}

// isCLIAction 判断本次启动是否只是一次性运维动作。
func isCLIAction(licenseFile, devices string) bool {
	return licenseFile != "" || devices != ""
}

// runCLIAction 借用已装配好的库完成一次运维动作，然后返回。
func runCLIAction(cfg *config.Config, licenseFile, devices string) error {
	res, err := bootstrap.BuildServer(cfg, bootstrap.Options{Logf: log.Printf})
	if err != nil {
		if bootstrap.IsNotConfigured(err) {
			return fmt.Errorf("server 尚未配置数据库：%w", err)
		}
		return fmt.Errorf("bootstrap failed: %w", err)
	}
	defer func() {
		if cerr := res.Close(); cerr != nil {
			log.Printf("close database: %v", cerr)
		}
	}()

	lanAdmin, _ := res.Apps.(usecase.LANAdmin)

	if licenseFile != "" {
		if lanAdmin == nil {
			return errors.New("当前装配不支持授权管理")
		}
		raw, err := os.ReadFile(licenseFile)
		if err != nil {
			return fmt.Errorf("读取授权文件 %s: %w", licenseFile, err)
		}
		st, err := lanAdmin.ImportLicense(raw, "cli")
		if err != nil {
			return fmt.Errorf("导入授权失败：%w", err)
		}
		log.Printf("授权已导入：客户=%s 类型=%s 客户端数=%d",
			st.Runtime.Customer, st.Runtime.Kind, st.Runtime.MaxClients)
		return nil
	}

	if lanAdmin == nil {
		return errors.New("当前装配不支持设备管理")
	}
	runDevicesCLI(lanAdmin, devices)
	return nil
}

// deviceGuardOf 只在 Service 实现了准入时才启用门禁。
//
// 用断言而不是直接传 svc：装配结果可能是别的实现（测试桩），
// 传 nil 的语义是"这台机器没有局域网准入"，与单机形态一致。
func deviceGuardOf(apps any) api.DeviceGuard {
	if g, ok := apps.(usecase.DeviceGuard); ok {
		return g
	}
	return nil
}

func readiness(res *bootstrap.Result) func() error {
	if res.Store == nil {
		return nil
	}
	return res.Store.Ping
}

// bootstrapTokenForStartup 决定本次启动的首建令牌。
//
// 只在"还没有任何用户"时生成：库已经有管理员了，令牌就没有意义，
// 继续把它留在磁盘上只是多一份可猜的凭据。
func bootstrapTokenForStartup(explicit string, apps usecase.Applications) string {
	if explicit != "" {
		return explicit
	}
	n, err := apps.UserCount()
	if err != nil {
		// 读不到用户数时**不**生成令牌：那说明库还没就绪，此时生成令牌
		// 会让客户抄一个本次启动根本用不上的码。
		log.Printf("lan: 读取用户数失败，跳过初始化令牌: %v", err)
		return ""
	}
	if n > 0 {
		return ""
	}
	tok, err := newBootstrapToken()
	if err != nil {
		log.Printf("lan: 生成初始化令牌失败: %v", err)
		return ""
	}
	path, err := writeBootstrapToken(paths.DataDir(), tok)
	if err != nil {
		log.Printf("lan: 初始化令牌 %s（写入文件失败: %v）", tok, err)
		return tok
	}
	log.Printf("lan: 空库尚未初始化 —— 初始化令牌 %s（已写入 %s）", tok, path)
	return tok
}

// runHealthcheck 探测本机 /readyz；0 = 就绪。
func runHealthcheck(addr string, cfg *config.Config) int {
	host := "127.0.0.1"
	port := "8080"
	target := addr
	if target == "" {
		target = cfg.Srv.Addr
	}
	if target != "" {
		if strings.HasPrefix(target, ":") {
			port = strings.TrimPrefix(target, ":")
		} else if h, p, err := splitHostPort(target); err == nil {
			// 监听在 0.0.0.0 时用回环探测；监听在具体地址时照用的端口。
			if h != "" && h != "0.0.0.0" && h != "::" {
				host = h
			}
			port = p
		}
	}
	url := "http://" + host + ":" + port + "/readyz"
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "未就绪：%v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "未就绪：%s 返回 %d\n", url, resp.StatusCode)
		return 1
	}
	fmt.Println("就绪")
	return 0
}

func splitHostPort(s string) (string, string, error) {
	if i := strings.LastIndex(s, ":"); i >= 0 {
		return s[:i], s[i+1:], nil
	}
	return "", s, fmt.Errorf("no port")
}

// logStartupAddresses 打印客户端可用的访问地址（FR-04 / R29）。
//
// 多网卡是局域网版最常见的连不上原因：主机上可能有 WiFi、有线、虚拟机网桥，
// 客户不知道填哪个。把全部候选列出来，并标注主机指纹，排障时一眼能对上。
func logStartupAddresses(listen, hostID string) {
	port := "8080"
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		port = listen[i+1:]
	}
	urls := netinfo.AccessURLs(port)
	if len(urls) == 0 {
		log.Printf("lan: 未发现可用的局域网 IPv4（客户端将无法连接，请检查网卡）")
		return
	}
	log.Printf("lan: 客户端可填写以下地址之一（同一局域网内）：")
	for _, u := range urls {
		log.Printf("lan:   %s", u)
	}
	if hostID != "" {
		log.Printf("lan: 主机设备指纹 %s（客户端如显示其它指纹，说明连的不是这台主机）", hostID)
	}
}

// newBootstrapToken 生成 8 位、去掉易混字符的令牌。
//
// 8 位足够：令牌只管"空库首建"这几分钟，且只在局域网可见；
// 去掉 0/O、1/I/l 是因为客户要从屏幕上抄到另一台电脑上输入。
func newBootstrapToken() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out), nil
}

// writeBootstrapToken 把令牌写到数据目录，供安装器/客户抄写。
func writeBootstrapToken(dir, tok string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("数据目录未配置")
	}
	path := filepath.Join(dir, "初始化令牌.txt")
	content := "RFERP 首次初始化令牌\n\n" + tok + "\n\n" +
		"用途：在第一台客户端上「创建管理员」时输入。\n" +
		"创建管理员成功后，本文件可以删除（服务器不会再校验它）。\n"
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// runDevicesCLI 提供"管理员被锁在外面"时的主机兜底（FR-30）。
// shortHash 安全截断指纹：数据异常时宁可少显示几位，也不要在 CLI 里 panic。
func shortHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}

func runDevicesCLI(admin usecase.LANAdmin, cmdline string) {
	if admin == nil {
		log.Fatal("当前装配不支持设备管理")
	}
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		log.Fatal("用法：-devices \"list\" 或 -devices \"revoke <指纹前12位>\"")
	}
	switch fields[0] {
	case "list":
		list, err := admin.ListDevices()
		if err != nil {
			log.Fatalf("读取设备列表失败：%v", err)
		}
		if len(list) == 0 {
			fmt.Println("（暂无设备）")
			return
		}
		fmt.Printf("%-14s %-8s %-9s %-20s %s\n", "指纹", "类型", "状态", "名称", "最近在线")
		for _, d := range list {
			fmt.Printf("%-14s %-8s %-9s %-20s %s\n",
				d.DeviceIDHash[:12], d.Kind, d.Status, d.Name,
				d.LastSeenAt.Format("2006-01-02 15:04"))
		}
	case "revoke", "delete":
		if len(fields) < 2 {
			log.Fatalf("用法：-devices \"%s <指纹前12位>\"", fields[0])
		}
		prefix := strings.ToLower(fields[1])
		list, err := admin.ListDevices()
		if err != nil {
			log.Fatalf("读取设备列表失败：%v", err)
		}
		var match []string
		for _, d := range list {
			if strings.HasPrefix(d.DeviceIDHash, prefix) {
				match = append(match, d.DeviceIDHash)
			}
		}
		switch len(match) {
		case 0:
			log.Fatalf("没有以 %q 开头的设备", prefix)
		case 1:
		default:
			log.Fatalf("前缀 %q 匹配到 %d 台设备，请提供更长的前缀", prefix, len(match))
		}
		if fields[0] == "revoke" {
			if err := admin.RevokeDevice(match[0], "cli"); err != nil {
				log.Fatalf("撤销失败：%v", err)
			}
			fmt.Printf("已撤销设备 %s\n", match[0][:12])
		} else {
			if err := admin.DeleteDevice(match[0], "cli"); err != nil {
				log.Fatalf("删除失败：%v", err)
			}
			fmt.Printf("已删除设备 %s（额度已释放）\n", match[0][:12])
		}
	default:
		log.Fatalf("未知子命令 %q；支持 list / revoke / delete", fields[0])
	}
}
