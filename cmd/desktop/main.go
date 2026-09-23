package main

import (
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"app/internal/api"
	"app/internal/config"
	"app/internal/logging"
	"app/internal/migrate"
	"app/internal/model"
	"app/internal/paths"
	"app/internal/repository"
	"app/internal/service"
	"app/internal/singleinstance"
	"app/internal/ui"
	"app/internal/update"
	"app/internal/usecase"
	"app/internal/winappid"
	"app/internal/winmsg"
)

var version = "dev"

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

func ensureMySQL(host, port, service string) {
	if !isLocalHost(host) {
		return
	}
	addr := net.JoinHostPort(host, port)
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err == nil {
		conn.Close()
		return
	}
	log.Printf("MySQL 未运行，尝试启动服务 %s...", service)
	cmd := exec.Command("cmd", "/c", "net", "start", service)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("启动 MySQL 服务失败: %v\n%s", err, string(out))
		// 第二次尝试用 sc start
		cmd2 := exec.Command("cmd", "/c", "sc", "start", service)
		if out2, err2 := cmd2.CombinedOutput(); err2 != nil {
			log.Printf("sc start 也失败: %v\n%s", err2, string(out2))
		}
	}
	// 等待 MySQL 就绪
	for i := 0; i < 30; i++ {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			log.Println("MySQL 已就绪")
			return
		}
		time.Sleep(time.Second)
	}
	log.Println("等待 MySQL 超时，将尝试连接...")
}

func isLocalHost(host string) bool {
	switch host {
	case "", "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

func main() {
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
	if !cfg.ModeChosen() {
		if cfg.Loaded() {
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
	log.Printf("run mode=local")
	if cfg.Loaded() {
		ensureMySQL(cfg.DB.Host, strconv.Itoa(cfg.DB.Port), cfg.MySQLService)
	}

	db, err := sqlx.Connect("mysql", cfg.DB.DSN)
	if err != nil {
		log.Printf("database connection failed: %v", err)
		ui.ShowSetup(a, cfg, func(newCfg *config.Config, newDB *sqlx.DB) {
			paths.SetDataDir(newCfg.DataDir)
			enterLocalDB(a, newCfg, newDB)
		})
		return
	}
	enterLocalDB(a, cfg, db)
}

func enterLocalDB(a fyne.App, cfg *config.Config, db *sqlx.DB) {
	log.Printf("database connected: %s@%s:%d/%s", cfg.DB.User, cfg.DB.Host, cfg.DB.Port, cfg.DB.DBName)
	res, err := migrate.Run(db, migrate.Options{
		DSN:           cfg.DB.DSN,
		MysqldumpPath: cfg.MysqldumpPath,
		BackupDir:     paths.BackupDir(),
	})
	if err != nil {
		log.Printf("migration failed: %v", err)
		winmsg.Error("RFERP 数据库升级失败",
			err.Error()+"\n\n升级前的备份（如有）已保留，请检查后重试。")
		return
	}
	if len(res.Applied) > 0 {
		log.Printf("migration applied: %v (backup: %s)", res.Applied, res.BackupPath)
	}

	apps := assembleApps(repository.New(db), cfg.DB.DSN, cfg.MysqldumpPath, cfg)

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

// assembleApps 是应用组装点（A4）：UI 只见 usecase.Applications，
// 具体 Service / Repository / 备份适配在此接线（D-013）。
func assembleApps(repo *repository.Repository, dsn, backupTool string, cfg *config.Config) usecase.Applications {
	return service.New(repo, dsn, backupTool, cfg)
}

func launchMain(a fyne.App, cfg *config.Config, svc usecase.Applications) {
	w := a.NewWindow("RFERP-仁风仓库管理系统 v" + version)
	w.SetIcon(ui.AppLogo())
	w.Resize(fyne.NewSize(1360, 860))
	w.CenterOnScreen()
	w.SetPadded(true)

	appUI := ui.NewApp(svc, cfg, w, func() {
		w.Close()
		ui.ShowLogin(a, svc, func(u *model.User) {
			log.Printf("login: %s (%s)", u.Username, u.Role)
			launchMain(a, cfg, svc)
		})
	})
	w.SetContent(appUI.BuildUI())
	w.Show()

	update.CleanupOld()
	updateURL := cfg.UpdateURL
	if v := os.Getenv("RFERP_UPDATE_URL"); v != "" {
		updateURL = v
	}
	if cfg.AutoUpdate && updateURL != "" {
		ui.StartUpdateCheck(w, updateURL, version)
	}
}
