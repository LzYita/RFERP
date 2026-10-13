// 首启数据库初始化向导（仅桌面形态）。
//
// 存在的理由：bootstrap.Build 在**没有配置时**会返回 not configured，
// 而业务接口此时一个都起不来。Fyne 版靠 internal/ui/setup.go 在窗口里问用户，
// Wails 版缺这一环——全新电脑上双击只会看到「not configured」然后退出。
//
// 为什么是独立页面而不是主界面的一个屏：向导跑在**服务层之前**，
// 此时没有 api.Server、没有会话、没有权限可谈。把「建第一个管理员」那套
// 状态机硬塞进来只会得到一堆永远为真的分支。一次性表单用独立页面更直白。
//
// 范围：本轮只做 SQLite（本地单机）。MySQL 属于服务器形态，
// 与「运行模式选择」一起待做——那一档本来就标记为本版本未开放。

package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	_ "github.com/go-sql-driver/mysql"

	"app/internal/api"
	"app/internal/bootstrap"
	"app/internal/config"
	"app/internal/dbsetup"
	"app/internal/mysqlfind"
	"app/internal/nativefiledialog"
	"app/internal/paths"
	"app/internal/webassets"
	"app/internal/winmsg"
)

// runSetupWizard 展示初始化向导并阻塞，直到用户完成或放弃。
//
// 返回 true 表示配置已写入，调用方应重新加载配置并重试 bootstrap。
// newSetupHandler 返回首启流程的 HTTP 处理器。
//
// 三个阶段共用同一个窗口，靠 resize 区分「这是哪一步」：
//
//	阶段 1 数据库配置  小窗口，填数据目录与存储方式
//	阶段 2 创建管理员  小窗口，账号 / 别名 / 两次密码
//	阶段 3 正常应用    恢复 1360x860
//
// 账号创建独立成一步而不是塞在配置页里，是因为两件事的心智负担完全不同：
// 配数据库是一次性的技术活，建账号是每天都要用的身份信息。把它们挤在一张
// 表单里，用户会以为填完数据库就结束了。
func newSetupHandler(base *config.Config, applied func()) http.Handler {
	// 只触发一次：用户可能重复点击保存，或页面 reload 后又发一次。
	var once sync.Once
	notify := func() { once.Do(applied) }

	mux := http.NewServeMux()
	// 目录浏览：调起系统原生文件夹选择框。
	//
	// 配置页是普通 HTML，没有 Wails 的运行时绑定，拿不到前端 API；
	// 但处理器本身在 Go 里，直接调 Win32 的 SHBrowseForFolder 即可
	// （internal/nativefiledialog 就是这么实现的，不依赖 Fyne）。
	//
	// 这比让用户手打路径可靠得多：C:\ 下的路径打错一个字符不会报错，
	// 只会在稍后"无法创建目录"时才暴露。
	mux.HandleFunc("POST /api/setup/pick-dir", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Title string `json:"title"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		title := strings.TrimSpace(in.Title)
		if title == "" {
			title = "请选择数据目录"
		}
		dir, ok := nativefiledialog.PickFolder(title)
		if !ok || dir == "" {
			// 用户取消不是错误，返回 ok:false 让前端保持原值。
			writeJSONStatus(w, 200, map[string]any{"ok": false})
			return
		}
		writeJSONStatus(w, 200, map[string]any{"ok": true, "dir": dir})
	})

	mux.HandleFunc("GET /api/setup/defaults", func(w http.ResponseWriter, r *http.Request) {
		// 把 MySQL 探测结果一并给出：旧版 setup.go 会在表单里显示
		// 「检测到本机 MySQL 服务」，用户据此决定填不填。缺了这一段，
		// 用户得自己知道本机装了 MySQL、服务名是什么。
		infos := mysqlfind.Detect()
		best := pickBestMySQL(infos)
		out := map[string]any{
			"dataDir":     defaultDataDir(base),
			"sqlitePath":  filepath.Join(defaultDataDir(base), config.DefaultSQLiteFileName),
			"version":     version,
			"machineName": os.Getenv("COMPUTERNAME"),
			"mysql": map[string]any{
				"port":        probePortOr(3306),
				"detected":    best != nil,
				"serviceName": "",
				"running":     false,
			},
		}
		if best != nil {
			m := out["mysql"].(map[string]any)
			m["serviceName"] = best.ServiceName
			m["running"] = best.Running
			m["dumpPath"] = best.MysqldumpPath
		}
		writeJSON(w, out)
	})

	// 测试连接：不写任何东西，只回答「能不能连上」。
	// 与保存分开是刻意的——用户填错密码时不该已经改了配置。
	mux.HandleFunc("POST /api/setup/test-mysql", func(w http.ResponseWriter, r *http.Request) {
		in, ok := decodeMySQLInput(w, r)
		if !ok {
			return
		}
		if err := pingMySQL(in); err != nil {
			writeJSONStatus(w, 200, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSONStatus(w, 200, map[string]any{"ok": true})
	})

	// 启动本机 MySQL 服务（需要时提权）。
	mux.HandleFunc("POST /api/setup/start-mysql-service", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Service string `json:"service"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Service) == "" {
			writeJSONStatus(w, 400, map[string]string{"error": "缺少服务名"})
			return
		}
		if err := mysqlfind.StartServiceElevated(in.Service); err != nil {
			writeJSONStatus(w, 200, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSONStatus(w, 200, map[string]any{"ok": true})
	})

	mux.HandleFunc("POST /api/setup/apply", func(w http.ResponseWriter, r *http.Request) {
		// 存储形态由前端选择决定：单机可选用 SQLite，也可直连 MySQL
		//（旧版 ShowSetup 两种都提供，本轮补齐，不能因为「形态不同」
		//  砍掉一台机器本来就跑 MySQL 的用户）。
		var in struct {
			Storage  string `json:"storage"`
			DataDir  string `json:"dataDir"`
			Host     string `json:"host"`
			Port     int    `json:"port"`
			User     string `json:"user"`
			Password string `json:"password"`
			DBName   string `json:"dbname"`
			Service  string `json:"service"`
			DumpPath string `json:"dumpPath"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONStatus(w, 400, map[string]string{"error": "请求无效"})
			return
		}

		cfg := *base
		// 数据目录是**公共项**：两种存储都用它放日志、备份、导出与凭证。
		// MySQL 用户也照样需要——业务数据在库里，但这些东西不在。
		if d := strings.TrimSpace(in.DataDir); d != "" {
			if err := ensureWritableDir(d); err != nil {
				writeJSONStatus(w, 400, map[string]string{"error": err.Error()})
				return
			}
			cfg.DataDir = d
		}

		if strings.EqualFold(strings.TrimSpace(in.Storage), config.StorageMySQL) {
			mi := mysqlInput{
				Host:     in.Host,
				Port:     in.Port,
				User:     in.User,
				Password: in.Password,
				DBName:   in.DBName,
				Service:  in.Service,
				DumpPath: in.DumpPath,
			}
			if err := applyMySQL(&cfg, mi, mi.Dedicated); err != nil {
				writeJSONStatus(w, 400, map[string]string{"error": err.Error()})
				return
			}
		} else {
			dir := strings.TrimSpace(in.DataDir)
			if dir == "" {
				writeJSONStatus(w, 400, map[string]string{"error": "数据目录不能为空"})
				return
			}
			// 在写配置之前就确认这个目录真的能用——否则用户填完才发现，
			// 已经把配置改坏了，再退回也退不回去。
			if err := ensureWritableDir(dir); err != nil {
				writeJSONStatus(w, 400, map[string]string{"error": err.Error()})
				return
			}
			cfg.DataDir = dir
			if err := cfg.SetStorage(config.StorageSQLite, filepath.Join(dir, config.DefaultSQLiteFileName)); err != nil {
				writeJSONStatus(w, 400, map[string]string{"error": err.Error()})
				return
			}
		}

		if err := cfg.Save(); err != nil {
			writeJSONStatus(w, 500, map[string]string{"error": "写入配置失败：" + err.Error()})
			return
		}
		writeJSONStatus(w, 200, map[string]string{"status": "ok"})
		// 响应先写出，再通知调用方：否则前端会以为请求卡住了。
		go notify()
	})
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// 刷新后可能停在这一页。配置一旦写成就直接送去建账号——
		// 否则用户会以为配置还没生效，又填一遍。
		if configPathExists() {
			http.Redirect(w, r, "/account", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(setupPageHTML))
	})
	// 阶段 2：数据库已连通但一个账号都没有时，进这一页创建第一个管理员。
	mux.HandleFunc("GET /account", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(setupAccountHTML))
	})
	mux.HandleFunc("POST /api/setup/create-admin", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Username    string `json:"username"`
			DisplayName string `json:"displayName"`
			Password    string `json:"password"`
			Confirm     string `json:"confirm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSONStatus(w, 400, map[string]string{"error": "请求无效"})
			return
		}
		// 两次密码不一致是最高频的失误，在服务端也挡一道，
		// 不依赖前端一定传对了。
		if in.Password != in.Confirm {
			writeJSONStatus(w, 400, map[string]string{"error": "两次输入的密码不一致"})
			return
		}
		if strings.TrimSpace(in.Username) == "" {
			writeJSONStatus(w, 400, map[string]string{"error": "请填写账号"})
			return
		}
		if err := createFirstAdmin(base, strings.TrimSpace(in.Username), in.Password, strings.TrimSpace(in.DisplayName)); err != nil {
			writeJSONStatus(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSONStatus(w, 200, map[string]string{"status": "ok"})
		// 账号有了，接下来该交给正常应用了。
		go notify()
	})
	return mux
}

// ---- MySQL 辅助 ----

type mysqlInput struct {
	Host      string
	Port      int
	User      string
	Password  string
	DBName    string
	Service   string
	DumpPath  string
	Dedicated bool
}

// decodeMySQLInput 解析并校验连接参数。
//
// 校验放在写配置之前：宁可让用户改到对为止，也不能先落一份连不上的配置。
func decodeMySQLInput(w http.ResponseWriter, r *http.Request) (mysqlInput, bool) {
	var in struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		User     string `json:"user"`
		Password string `json:"password"`
		DBName   string `json:"dbname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSONStatus(w, 400, map[string]string{"error": "请求无效"})
		return mysqlInput{}, false
	}
	out := mysqlInput{
		Host:     strings.TrimSpace(in.Host),
		Port:     in.Port,
		User:     strings.TrimSpace(in.User),
		Password: in.Password,
		DBName:   strings.TrimSpace(in.DBName),
	}
	if out.Host == "" {
		out.Host = "127.0.0.1"
	}
	if out.Port == 0 {
		out.Port = 3306
	}
	if out.User == "" || out.DBName == "" {
		writeJSONStatus(w, 400, map[string]string{"error": "用户名与数据库名不能为空"})
		return mysqlInput{}, false
	}
	if out.Port <= 0 || out.Port > 65535 {
		writeJSONStatus(w, 400, map[string]string{"error": "端口号无效"})
		return mysqlInput{}, false
	}
	return out, true
}

// applyMySQL 校验连通性并写入配置。
func applyMySQL(cfg *config.Config, in mysqlInput, dedicated bool) error {
	if in.Host == "" {
		in.Host = "127.0.0.1"
	}
	if in.Port == 0 {
		in.Port = 3306
	}
	if in.User == "" || in.DBName == "" {
		return errors.New("用户名与数据库名不能为空")
	}
	if in.Port <= 0 || in.Port > 65535 {
		return errors.New("端口号无效")
	}
	// 连不上就不要写配置。否则用户会得到一个「保存成功但启动失败」的假象。
	if err := pingMySQL(in); err != nil {
		return fmt.Errorf("无法连接 MySQL：%w", err)
	}

	user, pass := in.User, in.Password
	// 用 root 连接时另建专用账号：程序长期持有的是这个账号的凭据，
	// root 密码不留在本机的 config.json 里。与旧版 setup.go 的同名选项一致。
	if dedicated && strings.EqualFold(in.User, "root") {
		u, p, err := createDedicatedUser(in)
		if err != nil {
			// 专用账号建不出来不是致命问题——root 也能正常用。
			// 硬失败只会让一台本来好使的机器装不上。
			log.Printf("setup wizard: dedicated user skipped: %v", err)
		} else {
			user, pass = u, p
		}
	}

	cfg.SetDB(in.Host, in.Port, user, pass, in.DBName)
	if in.Service != "" {
		cfg.MySQLService = in.Service
	}
	if in.DumpPath != "" {
		cfg.MysqldumpPath = in.DumpPath
	}
	return cfg.SetStorage(config.StorageMySQL, "")
}

// createDedicatedUser 建一个专用账号并授予该库的权限。
func createDedicatedUser(in mysqlInput) (string, string, error) {
	pass, err := dbsetup.RandomPassword(24)
	if err != nil {
		return "", "", err
	}
	adminDSN := fmt.Sprintf("%s:%s@tcp(%s:%d)/?timeout=5s&charset=utf8mb4",
		in.User, in.Password, in.Host, in.Port)
	name := "rferp"
	if err := dbsetup.EnsureUser(adminDSN, name, pass, in.DBName); err != nil {
		return "", "", err
	}
	return name, pass, nil
}

// pingMySQL 尝试建库并连一次。
func pingMySQL(mi mysqlInput) error {
	if !dbsetup.ValidIdentifier(mi.DBName) {
		return fmt.Errorf("数据库名不合法")
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?timeout=5s&charset=utf8mb4",
		mi.User, mi.Password, mi.Host, mi.Port)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return err
	}
	// 库名可能不存在：建出来，否则后面的装配会以更隐晦的方式失败。
	if _, err := db.Exec("CREATE DATABASE IF NOT EXISTS `" +
		mi.DBName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		// 权限不足时不该判连接失败——服务器是通的，只是这个账号不能建库。
		log.Printf("setup wizard: create database %q skipped: %v", mi.DBName, err)
	}
	return nil
}

// pickBestMySQL 从探测结果里挑一个最可能的服务：优先运行中的。
func pickBestMySQL(infos []mysqlfind.Info) *mysqlfind.Info {
	for i := range infos {
		if infos[i].Running {
			return &infos[i]
		}
	}
	if len(infos) > 0 {
		return &infos[0]
	}
	return nil
}

func probePortOr(def int) int {
	if p := mysqlfind.ProbePort(); p > 0 {
		return p
	}
	return def
}

// setupCtx 由 OnStartup 写入，runtime 的窗口操作都需要它。
var setupCtx context.Context

// runFirstRunSetup 在 Wails 窗口内跑完整个首启流程，然后交棒给正常启动序列。
//
// 流程：
//  1. 起一个监听，后端暂时是配置页
//  2. 开 Wails 窗口（显示配置页）
//  3. 用户保存 → 回调里重新装配 → 成功后换成业务后端 → 刷新窗口
//  4. 之后就是正常的登录页
//
// 中途任何一步失败都直接退出：配置页已经显示在窗口里，再弹一个系统对话框
// 只会让用户面对两个互相矛盾的界面。
func runFirstRunSetup(base *config.Config) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		winmsg.Error("RFERP 无法启动", "无法监听本地端口："+err.Error())
		os.Exit(1)
	}

	token, terr := newStartupToken()
	if terr != nil {
		winmsg.Error("RFERP 无法启动", terr.Error())
		os.Exit(1)
	}
	backend := newSwappableBackend(http.NotFoundHandler())
	// applied 在配置写入后触发一次；此时窗口已经起来了，可以直接刷新它。
	backend.swap(newSetupHandler(base, func() {
		// ctx 从 Wails 的 OnStartup 拿（runtime 需要它定位窗口）。
		go finishFirstRunSetup(setupCtx, base, backend, token)
	}))

	// 首启阶段的 handler 是 cmd 侧自建的 mux，不经过 Server.Handler()，
	// 因此**不会**自动带上启动凭据闸门。必须显式套一层，否则在本机任何
	// 进程都能调用 /api/setup/apply 改写配置、/api/setup/create-admin
	// 建管理员——而首启窗口的默认宽度只有 720，用户根本不会注意到后台
	// 发生了什么。
	//
	// WebView 侧不受影响：下面的 embeddedServer 会给每个请求注入 Cookie。
	srv := &http.Server{
		Handler:           api.StartupGate(token, backend),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go srv.Serve(ln)
	defer srv.Close()

	proxy := &embeddedServer{
		target: &url.URL{Scheme: "http", Host: ln.Addr().String()},
		token:  token,
	}
	log.Printf("first-run setup: window on http://%s (loopback only)", ln.Addr().String())

	wails.Run(&options.App{
		Title:       "RFERP-仁风仓库管理系统 v" + version,
		Width:       720,
		Height:      620,
		AssetServer: &assetserver.Options{Handler: proxy.proxyHandler()},
		Windows:     &windows.Options{Messages: webviewMessages()},
		OnStartup:   func(ctx context.Context) { setupCtx = ctx },
	})
}

// finishFirstRunSetup 配置写好后：装配 → 决定下一步 → 换后端 → 刷新窗口。
//
// 装配成功后先问一次用户数：库里还没有账号就不该把人直接扔进登录页——
// 那里没有任何账号可输，只会让人以为软件坏了。
func finishFirstRunSetup(ctx context.Context, base *config.Config, backend *swappableBackend, token string) {
	// 给前端一点时间把「已保存」显示出来再切换，否则用户会以为按钮失灵了。
	time.Sleep(400 * time.Millisecond)

	cfg := config.Load()
	res, err := bootstrap.Build(cfg, bootstrap.Options{Logf: log.Printf})
	if err != nil {
		log.Printf("first-run setup: bootstrap after config failed: %v", err)
		winmsg.Error("RFERP 无法启动", err.Error())
		os.Exit(1)
	}
	// **不要**在这里 defer res.Close()：这个函数返回后业务服务仍在用它，
	// 关掉连接等于交接即断开。句柄交给进程退出时统一回收。
	log.Printf("first-run setup: bootstrapped storage=%s", res.Kind)

	n, err := res.Apps.UserCount()
	if err != nil {
		log.Printf("first-run setup: user count failed: %v", err)
		n = 1 // 查不到就别多挡一道，直接进登录页
	}
	if n == 0 {
		log.Printf("first-run setup: no users yet, showing first-admin form")
		backend.swap(newSetupHandler(base, func() {
			go finishFirstRunSetup(ctx, base, backend, token)
		}))
		if ctx != nil {
			resizeCentered(ctx, 460, 560)
			runtime.WindowReload(ctx)
		}
		return
	}

	apiSrv := api.New(res.Apps,
		api.WithVersion(version),
		api.WithStartupToken(token),
		api.WithUpdate(updateURLFor(cfg), version),
		api.WithRemember(filepath.Join(cfg.DataDir, "remember.bin")),
		api.WithStaticFS(webassets.FS(), webassets.Dir),
	)
	go apiSrv.StartSessionJanitor(context.Background())
	backend.swap(apiSrv.Handler())

	if ctx != nil {
		// 恢复主窗口尺寸：配置与建账号阶段都是小窗口，
		// 不还原的话应用会一直挤在一个 460x560 的小框里。
		resizeCentered(ctx, mainWinW(), mainWinH())
		runtime.WindowReload(ctx)
		log.Printf("first-run setup: handed over to normal startup")
	} else {
		log.Printf("first-run setup: backend swapped (headless, no window)")
	}
}

// resizeCentered 改窗口尺寸并重新居中。
//
// 只调 SetSize 的话，Windows 会保持左上角不动：小窗口(460x560)变大后，
// 原左上角落在屏幕中央，整窗偏向右下，看着像没对齐。
func resizeCentered(ctx context.Context, w, h int) {
	runtime.WindowSetSize(ctx, w, h)
	runtime.WindowCenter(ctx)
}

// 主窗口尺寸。与 main 里 wails.Run 的 Width/Height 同源，
// 交接时用它把窗口还原回去。
func mainWinW() int { w, _ := fitWindow(1360, 860); return w }
func mainWinH() int { _, h := fitWindow(1360, 860); return h }

// configPathExists 判断配置是否已经写过。
//
// 用于「刷新时该显示哪一页」：写过了就只剩建账号这一步。
// 位置与 config.Save() 的选择一致——它优先写在 exe 同级（可写时），
// 否则写 %APPDATA%\RFERP\config.json。
func configPathExists() bool {
	if exe, err := os.Executable(); err == nil {
		if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "config.json")); err == nil {
			return true
		}
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		if _, err := os.Stat(filepath.Join(dir, "RFERP", "config.json")); err == nil {
			return true
		}
	}
	return false
}

// createFirstAdmin 建第一个管理员。
//
// 不经过 api.Server：此刻后端还是配置页，没有会话体系可用。
// 直接走用例层，与 Fyne 版 showCreateAdmin 同一路径。
func createFirstAdmin(base *config.Config, username, password, displayName string) error {
	cfg := config.Load()
	res, err := bootstrap.Build(cfg, bootstrap.Options{Logf: log.Printf})
	if err != nil {
		return fmt.Errorf("无法连接数据库：%w", err)
	}
	defer res.Close()
	if _, err := res.Apps.CreateInitialAdmin(username, password, displayName); err != nil {
		return err
	}
	return nil
}

func updateURLFor(cfg *config.Config) string {
	u := cfg.UpdateURL
	if v := os.Getenv("RFERP_UPDATE_URL"); v != "" {
		u = v
	}
	if !cfg.AutoUpdate {
		return ""
	}
	return u
}

// defaultDataDir 给出建议的数据目录。
//
// **默认指向 exe 同级目录**，与旧版（Fyne）行为一致：装在
// %LOCALAPPDATA%\Programs\RFERP 时数据就在其下的 data\。
//
// 这个选择是有意的，不是疏忽：低权限安装本来就落在用户目录下，数据跟着
// 程序走符合"绿色软件"的直觉，也让整套目录能整体拷贝/备份。
//
// 用户可以随时改——向导里可填任意路径，之后也能在 config.json 里改。
// 需要留意的是：既然默认在安装目录里，**卸载会连同数据一起删掉**，
// 这是该默认值的代价，向导页上已经写明。
func defaultDataDir(base *config.Config) string {
	if d := strings.TrimSpace(base.DataDir); d != "" {
		return d
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "data")
	}
	return paths.DataDir()
}

// ensureWritableDir 建目录并落一个探针文件，确认真的可写。
func ensureWritableDir(dir string) (err error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("无法创建目录：%w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("无法打开目录：%w", err)
	}
	defer root.Close()
	name := ".rferp-write-test-" + rand.Text()
	probe, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("目录不可写：%w", err)
	}
	defer func() { err = errors.Join(err, root.Remove(name)) }()
	_, writeErr := probe.WriteString("ok")
	return errors.Join(writeErr, probe.Close())
}

func randomToken() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, v any) { writeJSONStatus(w, 200, v) }

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// setupAccountHTML 是首启第二步：创建第一个管理员。
//
// 独立于配置页的原因见 newSetupHandler 的注释：配数据库与定身份是两件事。
// 字段为 账号 / 别名 / 密码 / 确认密码，与旧版 showCreateAdmin 一致。
const setupAccountHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>创建管理员</title>
<style>
:root{--pri:#0f766e;--pri-h:#115e59;--sub:#e6f2f0;--sb:#a7d3ce;
--fg:#17211f;--mu:#5c6b68;--bd:#d7e0de;--cv:#f1f5f4;--sf:#fff;--er:#c23a3a;--wn:#b47012}
*{box-sizing:border-box}
body{margin:0;height:100vh;display:grid;place-items:center;background:var(--sub);
font:14px/1.6 "Microsoft YaHei",-apple-system,"Segoe UI",sans-serif;color:var(--fg)}
.card{width:min(420px,92vw);background:var(--sf);border:1px solid var(--sb);
border-radius:12px;padding:26px 28px;box-shadow:0 8px 28px rgba(0,0,0,.08)}
h1{margin:0 0 4px;font-size:18px}
.lead{margin:0 0 20px;font-size:12.5px;color:var(--mu)}
label{display:block;margin:0 0 5px;font-size:12px;color:var(--mu)}
.req{color:var(--er)}
input{width:100%;height:33px;padding:0 10px;border:1px solid var(--bd);border-radius:8px;
font-size:13px;outline:none}
input:focus{border-color:var(--pri)}
input.bad{border-color:var(--er)}
.f{margin-bottom:14px}
.pw{position:relative}
.pw input{padding-right:38px}
.pw button{position:absolute;right:4px;top:5px;width:26px;height:23px;padding:0;
border:0;background:none;color:var(--mu);cursor:pointer}
.pw button:hover{color:var(--pri)}
/* 关掉 Edge/WebView2 自带的显示密码按钮：它在字段有内容时才出现，
   会和我们自己的眼睛图标并排出现（原生睁眼 + 我们的闭眼）。 */
input[type=password]::-ms-reveal,input[type=password]::-ms-clear,
input[type=text]::-ms-reveal,input[type=text]::-ms-clear{display:none}
.hint{margin:5px 0 0;font-size:11.5px;color:var(--mu)}
.hint.wn{color:var(--wn)}
.err{margin:12px 0 0;padding:8px 10px;border:1px solid rgba(194,58,58,.3);
background:#fde8e8;color:var(--er);border-radius:6px;font-size:12px;display:none}
button{width:100%;height:34px;margin-top:6px;border:0;border-radius:8px;background:var(--pri);
color:#fff;font-size:13px;font-weight:600;cursor:pointer}
button:hover{background:var(--pri-h)}
button:disabled{opacity:.5}
</style></head><body>
<div class="card">
  <h1>创建第一个管理员</h1>
  <p class="lead">数据库已就绪。这台机器上还没有任何账号，请先创建。</p>
  <form id="f" autocomplete="off">
    <div class="f">
      <label for="u">账号 <span class="req">*</span></label>
      <input id="u" autofocus autocomplete="off">
    </div>
    <div class="f">
      <label for="d">别名（选填）</label>
      <input id="d" placeholder="例如：仓库主管">
    </div>
    <div class="f">
      <label for="p">密码 <span class="req">*</span></label>
      <div class="pw">
        <input id="p" type="password" autocomplete="new-password">
        <button type="button" id="tg1" aria-label="显示密码" title="显示密码"></button>
      </div>
      <p class="hint" id="pw"></p>
    </div>
    <div class="f">
      <label for="p2">确认密码 <span class="req">*</span></label>
      <div class="pw">
        <input id="p2" type="password" autocomplete="new-password">
        <button type="button" id="tg2" aria-label="显示确认密码" title="显示确认密码"></button>
      </div>
      <p class="hint" id="pw2"></p>
    </div>
    <div class="err" id="err"></div>
    <button type="submit" id="go">创建并进入</button>
  </form>
</div>
<script>
const $=id=>document.getElementById(id);

// 眼睛图标（内联 SVG，避免图片请求）。closed=true 画闭眼。
const eyeSVG = closed =>
  '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" '+
  'stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">'+
  (closed
    ? '<path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94"/>'+
      '<path d="M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19"/>'+
      '<path d="M14.12 14.12a3 3 0 1 1-4.24-4.24"/><line x1="2" y1="2" x2="22" y2="22"/>'
    : '<path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z"/><circle cx="12" cy="12" r="3"/>')+
  '</svg>';

// 两个框各自一个开关，互不影响：共用状态会让点一个眼睛把两个框都显出来。
function bindToggle(inputId, btnId, labelId){
  const input=$(inputId), btn=$(btnId);
  let shown=false;
  function paint(){
    // 图标表示**当前状态**：不可见时是闭眼。
    btn.innerHTML = eyeSVG(!shown);
    const l = shown ? '隐藏' : '显示';
    btn.setAttribute('aria-label', labelId ? l+'确认密码' : l+'密码');
    btn.title = btn.getAttribute('aria-label');
  }
  paint();
  btn.onclick=()=>{ shown=!shown; input.type = shown?'text':'password'; paint(); };
}
bindToggle('p','tg1',false);
bindToggle('p2','tg2',true);

$('u').oninput=()=>{ if(!$('d').value) $('d').placeholder=$('u').value||'例如：仓库主管'; };
// 即时提示，不等服务端拒绝
$('p').oninput=()=>{
  const n=$('p').value.length;
  $('pw').textContent = n>0 && n<4 ? '密码至少 4 位' : '';
  $('pw').className = 'hint'+(n>0&&n<4?' wn':'');
};
$('p2').oninput=()=>{
  const bad = $('p2').value.length>0 && $('p2').value!==$('p').value;
  $('p2').className = bad?'bad':'';
  $('pw2').textContent = bad?'两次输入的密码不一致':'';
  $('pw2').className = bad?'hint wn':'hint';
};
$('f').onsubmit=async e=>{
  e.preventDefault();
  $('err').style.display='none';
  const username=$('u').value.trim(), password=$('p').value, confirm=$('p2').value;
  if(!username){$('err').textContent='请填写账号';$('err').style.display='block';return;}
  if(!password){$('err').textContent='请填写密码';$('err').style.display='block';return;}
  if(password!==confirm){$('err').textContent='两次输入的密码不一致';$('err').style.display='block';return;}
  $('go').disabled=true;$('go').textContent='正在创建…';
  const r=await fetch('/api/setup/create-admin',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({username:username,displayName:$('d').value.trim(),password:password,confirm:confirm})});
  const j=await r.json();
  if(!r.ok){$('err').textContent=j.error||'创建失败';$('err').style.display='block';
    $('go').disabled=false;$('go').textContent='创建并进入';return;}
  $('go').textContent='已创建，正在进入…';
  // 后端会在创建成功后切换到业务服务，这里刷新即可
  setTimeout(()=>location.reload(),800);
};
</script></body></html>`

// setupPageHTML 是一次性配置表单。
//
// 独立于主前端：此刻业务服务还没起来，把 React 应用拉起来只会多一层无谓的
// 依赖（构建产物、路由、状态）。一个自包含的表单更短也更好排查。
const setupPageHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>RFERP 首次配置</title>
<style>
:root{--pri:#0f766e;--pri-h:#115e59;--sub:#e6f2f0;--sb:#a7d3ce;
--fg:#17211f;--mu:#5c6b68;--bd:#d7e0de;--cv:#f1f5f4;--sf:#fff;--er:#c23a3a}
*{box-sizing:border-box}
body{margin:0;height:100vh;display:grid;place-items:center;background:var(--sub);
font:14px/1.6 "Microsoft YaHei",-apple-system,"Segoe UI",sans-serif;color:var(--fg)}
.card{width:min(560px,92vw);background:var(--sf);border:1px solid var(--sb);
border-radius:12px;padding:28px 30px;box-shadow:0 8px 28px rgba(0,0,0,.08)}
h1{margin:0 0 6px;font-size:19px}
.lead{margin:0 0 22px;font-size:12.5px;color:var(--mu)}
label{display:block;margin:0 0 6px;font-size:12px;color:var(--mu)}
input{width:100%;height:34px;padding:0 10px;border:1px solid var(--bd);border-radius:8px;
font-size:13px;outline:none}
input:focus{border-color:var(--pri)}
.hint{margin:6px 0 0;font-size:11.5px;color:var(--mu)}
.err{margin:12px 0 0;padding:8px 10px;border:1px solid rgba(194,58,58,.3);
background:#fde8e8;color:var(--er);border-radius:6px;font-size:12px;display:none}
button{margin-top:20px;height:34px;padding:0 18px;border:0;border-radius:8px;
background:var(--pri);color:#fff;font-size:13px;font-weight:600;cursor:pointer}
button:hover{background:var(--pri-h)}
button:disabled{opacity:.5;cursor:default}
.foot{margin-top:16px;font-size:11px;color:var(--mu);text-align:center}
.tabs{display:flex;gap:6px;margin-bottom:18px}
.tab{flex:1;height:32px;margin:0;border:1px solid var(--bd);background:var(--sf);
color:var(--mu);font-size:12.5px;font-weight:600;cursor:pointer;border-radius:8px}
.tab.on{background:var(--pri);border-color:var(--pri);color:#fff}
.row{display:flex;gap:10px}
.f{flex:1}
.f.sm{flex:0 0 92px}
.pickrow{display:flex;gap:8px}
.pickrow input{flex:1}
button.browse{width:auto;height:34px;margin:0;padding:0 14px;background:var(--sf);
  border:1px solid var(--bd);color:var(--mu);font-size:12.5px;font-weight:500}
button.browse:hover{border-color:var(--pri);color:var(--pri)}
.hint2{margin:0 0 14px;padding:7px 10px;background:var(--cv);border-radius:6px;font-size:11.5px;color:var(--mu)}
.det{display:none;margin-bottom:12px;padding:8px 10px;border:1px solid var(--sb);
background:var(--sub);border-radius:6px;font-size:12px;color:var(--mu)}
.acts{display:flex;gap:8px;margin-top:12px}
button.sec{height:28px;margin:0;padding:0 12px;background:var(--sf);
border:1px solid var(--bd);color:var(--mu);font-size:12px;font-weight:500}
.chk{display:flex;align-items:center;gap:6px;margin-top:14px;font-size:12px;color:var(--fg)}
.chk input{width:auto;height:auto;margin:0}
</style></head><body>
<div class="card">
  <h1>首次配置</h1>
  <p class="lead">这是 RFERP 第一次运行。请选择数据库与数据存放位置，之后可以随时在设置里更改。</p>

  <label for="dir">数据目录</label>
  <div class="pickrow">
    <input id="dir" autofocus>
    <button class="browse" type="button" id="pick">浏览…</button>
  </div>
  <p class="hint">存放日志、备份、导出文件与登录凭证。默认在程序目录下，便于整体拷贝；
    <b>卸载程序会一并删除该目录</b>，请勿存放唯一副本。</p>

  <div class="tabs">
    <button class="tab on" data-t="sqlite">本机存储（SQLite）</button>
    <button class="tab" data-t="mysql">MySQL 数据库</button>
  </div>

  <!-- SQLite：单机使用，默认且最省事 -->
  <div id="p-sqlite">
    <p class="hint2">业务数据存为 <b id="sqlitePathText"></b> 下的 SQLite 数据库文件。</p>
  </div>

  <!-- MySQL：单机也可以用 MySQL，对齐旧版 setup.go 的表单 -->
  <div id="p-mysql" hidden>
    <div class="det" id="det"></div>
    <div class="row">
      <div class="f"><label for="host">主机</label><input id="host" value="127.0.0.1"></div>
      <div class="f sm"><label for="port">端口</label><input id="port" value="3306"></div>
    </div>
    <div class="row">
      <div class="f"><label for="user">用户名</label><input id="user" value="root"></div>
      <div class="f"><label for="dbname">数据库名</label><input id="dbname" value="appliancedb"></div>
    </div>
    <label for="pass">密码</label>
    <input id="pass" type="password">
    <label class="chk"><input type="checkbox" id="dedicated" checked> 用 root 连接时，自动创建专用账号（推荐）</label>
    <div class="acts">
      <button class="sec" id="useLocal">使用本机 MySQL</button>
      <button class="sec" id="startSvc">启动服务</button>
      <button class="sec" id="test">测试连接</button>
    </div>
    <p class="hint" id="testOut">数据库不存在时会自动创建。勾选上面一项后，程序会另建一个专用账号并把密码写进配置，root 密码不留在本机。</p>
  </div>

  <div class="err" id="err"></div>
  <button id="go">保存并继续</button>
  <div class="foot">仁风仓库管理系统 · <span id="ver"></span> · 数据全部保存在本机</div>
</div>
<script>
const $=id=>document.getElementById(id);
let mode='sqlite', svc='', dump='';

fetch('/api/setup/defaults').then(r=>r.json()).then(d=>{
  $('dir').value=d.dataDir; $('ver').textContent='v'+d.version;
  $('sqlitePathText').textContent=d.sqlitePath||(d.dataDir+'/rferp.db');
  const m=d.mysql||{};
  if(m.port) $('port').value=m.port;
  if(m.detected){
    svc=m.serviceName||''; dump=m.dumpPath||'';
    $('det').style.display='block';
    $('det').textContent='已检测到本机 MySQL 服务：'+svc+(m.running?'（运行中）':'（未运行）');
    $('startSvc').style.display=m.running?'none':'inline-block';
  } else {
    $('det').style.display='block';
    $('det').textContent='未检测到本机 MySQL 服务，可手动填写远程地址。';
    $('startSvc').style.display='none';
  }
});

// 浏览按钮：调系统原生文件夹选择框
$('pick').onclick=async()=>{
  $('pick').disabled=true;
  const r=await fetch('/api/setup/pick-dir',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({title:'请选择数据目录'})});
  const j=await r.json();
  $('pick').disabled=false;
  if(j.ok) $('dir').value=j.dir;
};

// 切页签
document.querySelectorAll('.tab').forEach(b=>b.onclick=()=>{
  document.querySelectorAll('.tab').forEach(x=>x.classList.toggle('on',x===b));
  mode=b.dataset.t;
  $('p-sqlite').hidden = mode!=='sqlite';
  $('p-mysql').hidden  = mode!=='mysql';
});

$('useLocal').onclick=()=>{
  $('host').value='127.0.0.1';
  if(svc){ $('det').textContent='已套用本机 MySQL 服务：'+svc; $('startSvc').style.display='inline-block'; }
};
$('startSvc').onclick=async()=>{
  $('testOut').textContent='正在启动服务…';
  const r=await fetch('/api/setup/start-mysql-service',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify({service:svc})});
  const j=await r.json();
  $('testOut').textContent = j.ok ? '服务已启动。' : ('启动失败：'+j.error);
};
function mysqlBody(){
  return {host:$('host').value.trim(), port:parseInt($('port').value,10)||0,
          user:$('user').value.trim(), password:$('pass').value, dbname:$('dbname').value.trim()};
}
$('test').onclick=async()=>{
  $('testOut').textContent='正在测试连接…';
  const r=await fetch('/api/setup/test-mysql',{method:'POST',headers:{'Content-Type':'application/json'},
    body:JSON.stringify(mysqlBody())});
  const j=await r.json();
  $('testOut').textContent = j.ok ? '连接成功。' : ('连接失败：'+j.error);
};

$('go').onclick=async()=>{
  $('err').style.display='none';
  let body;
  if(mode==='sqlite'){
    const dir=$('dir').value.trim();
    if(!dir){$('err').textContent='数据目录不能为空';$('err').style.display='block';return;}
    body={storage:'sqlite', dataDir:dir};
  } else {
    const m=mysqlBody();
    if(!m.user||!m.dbname){$('err').textContent='用户名与数据库名不能为空';$('err').style.display='block';return;}
    body={storage:'mysql', service:svc, dumpPath:dump,
          dedicated:document.getElementById('dedicated').checked,
          host:m.host, port:m.port,
          user:m.user, password:m.password, dbname:m.dbname};
  }
  $('go').disabled=true;$('go').textContent='正在保存…';
  try{
    const r=await fetch('/api/setup/apply',{method:'POST',headers:{'Content-Type':'application/json'},
      body:JSON.stringify(body)});
    const j=await r.json();
    if(!r.ok)throw new Error(j.error||'保存失败');
    $('go').textContent='已保存，即将启动…';
    // 让界面把"成功"显示出来再跳转，避免用户以为卡死了
    setTimeout(()=>location.reload(),600);
  }catch(e){
    $('err').textContent=e.message;$('err').style.display='block';
    $('go').disabled=false;$('go').textContent='保存并继续';
  }
};
</script></body></html>`
