package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"github.com/jmoiron/sqlx"

	"app/internal/api"
	"app/internal/bootstrap"
	"app/internal/config"
	"app/internal/logging"
	"app/internal/model"
	"app/internal/mysqlfind"
	"app/internal/paths"
	"app/internal/singleinstance"
	"app/internal/ui"
	"app/internal/update"
	"app/internal/usecase"
	"app/internal/winappid"
	"app/internal/winmsg"
)

var version = "dev"

// serverModeEnabled 控制「连接服务器」运行模式是否对用户开放。
//
// v1.2.0 未开放：服务器程序尚未随安装包发布，也没有应用内切换入口，
// 用户一旦选中就会走进死胡同（地址填了也连不上、备份/恢复等功能不可用）。
// 代码本身保留可用（cmd/server、api.Client 均可正常工作），待前置条件具备后
// 把这里改为 true 即可重新开放。
const serverModeEnabled = false

func init() {
	// TTC 字体 Fyne 可能不兼容，优先用 TTF
	fonts := []string{
		"C:\\Windows\\Fonts\\simhei.ttf",
		"C:\\Windows\\Fonts\\msyh.ttc",
		"C:\\Windows\\Fonts\\simsun.ttc",
	}
	for _, f := range fonts {
		if _, err := os.Stat(f); err == nil {
			os.Setenv("FYNE_FONT", f)
			break
		}
	}
}

// ensureMySQL 在连库前确保本机 MySQL 已就绪：探测端口，不通则启动对应服务。
//
// 启动 Windows 服务需要管理员权限。已提权时直接用 SCM API 启动；未提权时
// 只把这一条命令通过 UAC 提权执行（见 mysqlfind.StartServiceElevated），
// 因此正常使用时不会出现提权提示，只有 MySQL 确实没在运行时才弹一次 UAC。
//
// 返回值说明启动是否成功，供调用方决定是提示用户还是继续尝试连接。
func ensureMySQL(host, port, service string) error {
	if !isLocalHost(host) {
		return nil
	}
	addr := net.JoinHostPort(host, port)
	if portOpen(addr) {
		return nil
	}
	if service == "" {
		return errors.New("未配置 MySQL 服务名")
	}

	log.Printf("MySQL 未运行，尝试启动服务 %s...", service)
	err := mysqlfind.StartService(service)
	if err != nil {
		// 权限不足：改用 UAC 提权启动（会弹一次用户确认）
		var se *mysqlfind.StartError
		if errors.As(err, &se) && se.NeedsAdmin {
			log.Printf("启动 MySQL 服务需要管理员权限，改用 UAC 提权：%v", err)
			if err2 := mysqlfind.StartServiceElevated(service); err2 != nil {
				return err2
			}
		} else {
			return err
		}
	}

	// 等待 MySQL 就绪
	for i := 0; i < 30; i++ {
		if portOpen(addr) {
			log.Println("MySQL 已就绪")
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("等待 MySQL 就绪超时（%s）", addr)
}

func portOpen(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func isLocalHost(host string) bool {
	switch host {
	case "", "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

func main() {
	// 提权子进程：本次启动只为拉起 MySQL 服务，随后立即退出。
	// 必须放在最前面——不能抢单实例锁、不能建窗口、不能碰配置。
	if handled, err := mysqlfind.RunServiceStartIfRequested(os.Args); handled {
		if err != nil {
			log.Printf("elevated service start failed: %v", err)
			os.Exit(2)
		}
		os.Exit(0)
	}

	winappid.Set("LzYita.RFERP")

	// 更新后重启时，旧进程可能仍在退出中，稍等它释放单实例锁。
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
	log.Printf("RFERP %s starting", version)

	a := app.New()
	a.Settings().SetTheme(ui.NewTheme())
	a.SetIcon(ui.AppLogo())

	// D3：首启选定运行模式并持久化；之后不再询问。
	// v1.1 及更早只有「本机 + MySQL」：已有配置视为 Local，避免升级后误弹选型。
	// v1.2.0：连接服务器模式尚未对用户开放（见 serverModeEnabled），
	// 因此不再弹选型，全新安装也直接进入本机模式。
	if !cfg.ModeChosen() {
		if cfg.Loaded() || !serverModeEnabled {
			if err := cfg.SetRunMode(config.ModeLocal, ""); err != nil {
				log.Printf("auto local mode: %v", err)
			}
			enterAfterMode(a, cfg, config.ModeLocal)
			a.Run()
			return
		}
		ui.ShowRunModePicker(a, cfg, func(mode string) {
			enterAfterMode(a, config.Load(), mode)
		})
		a.Run()
		return
	}
	enterAfterMode(a, cfg, cfg.Mode)
	a.Run()
}

func enterAfterMode(a fyne.App, cfg *config.Config, mode string) {
	if mode == config.ModeClient {
		enterClientMode(a, cfg)
		return
	}
	enterLocalMode(a, cfg)
}

func enterClientMode(a fyne.App, cfg *config.Config) {
	if !serverModeEnabled {
		// 本版本未开放。不静默进入半可用状态（能登录，但备份/恢复等功能不可用），
		// 而是明确告知后退出。只有手工改过 config.json 才会走到这里。
		log.Printf("run mode=client refused: server mode is not enabled in this build")
		winmsg.Error("RFERP",
			"本版本未开放「连接服务器」模式。\n\n"+
				"该功能尚未随安装包提供服务器程序，也没有应用内切换入口，暂不开放。\n"+
				"请使用「本机」模式。")
		return
	}
	log.Printf("run mode=client server=%s", cfg.ServerURL)
	cli := api.NewClient(cfg.ServerURL)
	var apps usecase.Applications = cli
	if u, ok := ui.TryAutoLogin(apps); ok {
		log.Printf("auto login: %s", u.Username)
		launchMain(a, cfg, apps)
		return
	}
	ui.ShowLogin(a, apps, func(u *model.User) {
		log.Printf("login: %s (%s)", u.Username, u.Role)
		launchMain(a, cfg, apps)
	})
}

func enterLocalMode(a fyne.App, cfg *config.Config) {
	// 装配（选通道 → 连库 → 迁移 → 构造用例）已下沉到 internal/bootstrap，
	// 与 cmd/server 共用同一份实现。本函数只负责把失败翻译成对应的界面。
	res, err := bootstrap.Build(cfg, bootstrap.Options{
		EnsureMySQL: ensureMySQL,
		Logf:        log.Printf,
	})
	if err != nil {
		showLocalBuildError(a, cfg, err)
		return
	}
	if len(res.Applied) > 0 {
		log.Printf("storage=%s migration applied: %v", res.Kind, res.Applied)
	}
	launchLocalUI(a, cfg, res.Apps)
}

// showLocalBuildError 把装配错误映射到既有的界面上。
//
// 三类必须分开，不能合并成一句「启动失败」：
//   - 尚未配置 → 配置向导（用户需要做选择）
//   - 已配置但连不上 → 「数据库不可用」（用户需要知道真实原因并能重试）
//   - SQLite 各阶段故障 → 直接报错（路径已确定，不存在「配置」问题）
func showLocalBuildError(a fyne.App, cfg *config.Config, err error) {
	log.Printf("bootstrap failed: %v", err)

	// 尚未配置：引导走配置向导。
	if bootstrap.IsNotConfigured(err) {
		ui.ShowSetup(a, cfg, func(newCfg *config.Config, _ *sqlx.DB) {
			paths.SetDataDir(newCfg.DataDir)
			// 向导已经落盘配置，重新装配即可；不再复用向导握在手里的连接，
			// 以保证本机与 server 走的是同一条装配路径。
			enterLocalMode(a, newCfg)
		})
		return
	}

	// SQLite：路径已确定，没有「重新配置」这条路可走。
	if cfg.IsSQLite() {
		var title string
		switch bootstrap.StageOf(err) {
		case bootstrap.StageSQLitePath:
			title = "RFERP 无法确定本机数据库路径"
		case bootstrap.StageSQLiteMigrate:
			title = "RFERP 数据库升级失败"
		default:
			title = "RFERP 无法打开本机数据库"
		}
		winmsg.Error(title, err.Error())
		return
	}

	// MySQL 已配置却连不上：保留「启动服务亦失败」这一条线索，
	// 它和「连不上」是两个不同的原因，用户要分别看到。
	var startErr error
	var ce *bootstrap.ConnectionError
	if errors.As(err, &ce) {
		startErr = ce.EnsureErr
	}
	ui.ShowDatabaseUnavailable(a, cfg, err.Error(), startErr, func() {
		enterLocalMode(a, config.Load())
	})
}

func launchLocalUI(a fyne.App, cfg *config.Config, apps usecase.Applications) {
	if u, ok := ui.TryAutoLogin(apps); ok {
		log.Printf("auto login: %s", u.Username)
		launchMain(a, cfg, apps)
		return
	}
	ui.ShowLogin(a, apps, func(u *model.User) {
		log.Printf("login: %s (%s)", u.Username, u.Role)
		launchMain(a, cfg, apps)
	})
}

func launchMain(a fyne.App, cfg *config.Config, svc usecase.Applications) {
	w := a.NewWindow("RFERP-仁风仓库管理系统 v" + version)
	w.SetIcon(ui.AppLogo())
	w.Resize(fyne.NewSize(1360, 860))
	w.CenterOnScreen()
	w.SetPadded(true)

	update.CleanupOld()
	updateURL := cfg.UpdateURL
	if v := os.Getenv("RFERP_UPDATE_URL"); v != "" {
		updateURL = v
	}

	// 设置页要显示版本号并支持手动查更新，所以这两项要传进 UI 层。
	appUI := ui.NewAppWithVersion(svc, cfg, w, func() {
		w.Close()
		ui.ShowLogin(a, svc, func(u *model.User) {
			log.Printf("login: %s (%s)", u.Username, u.Role)
			launchMain(a, cfg, svc)
		})
	}, version, updateURL)
	w.SetContent(appUI.BuildUI())
	w.Show()

	if cfg.AutoUpdate && updateURL != "" {
		ui.StartUpdateCheck(w, updateURL, version)
	}
}
