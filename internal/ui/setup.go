package ui

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/jmoiron/sqlx"

	"app/internal/config"
	"app/internal/dbsetup"
	"app/internal/mysqlfind"
)

const mysqlDownloadURL = "https://dev.mysql.com/downloads/installer/"

func ShowSetup(a fyne.App, base *config.Config, onReady func(*config.Config, *sqlx.DB)) {
	w := a.NewWindow("RFERP 首次配置")
	w.Resize(fyne.NewSize(540, 600))
	w.CenterOnScreen()

	infos := mysqlfind.Detect()
	probed := mysqlfind.ProbePort()
	best := pickBest(infos)

	port := probed
	if port == 0 {
		port = 3306
	}

	host := widget.NewEntry()
	host.SetText("127.0.0.1")
	portE := widget.NewEntry()
	portE.SetText(strconv.Itoa(port))
	user := widget.NewEntry()
	user.SetText(defaultUser(base))
	pass := widget.NewPasswordEntry()
	pass.SetText(base.DB.Password)
	dbname := widget.NewEntry()
	dbname.SetText(defaultDB(base))
	dedicated := widget.NewCheck("使用 root 连接时，自动创建专用账号（推荐）", nil)
	dedicated.SetChecked(true)

	serviceName := ""
	dumpPath := ""
	if best != nil {
		serviceName = best.ServiceName
		dumpPath = best.MysqldumpPath
	}

	detectLabel := widget.NewLabel(detectText(best, port))
	detectLabel.Wrapping = fyne.TextWrapWord

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	build := func() (*config.Config, error) {
		p, err := strconv.Atoi(strings.TrimSpace(portE.Text))
		if err != nil || p <= 0 || p > 65535 {
			return nil, fmt.Errorf("端口号无效")
		}
		if strings.TrimSpace(host.Text) == "" || strings.TrimSpace(user.Text) == "" || strings.TrimSpace(dbname.Text) == "" {
			return nil, fmt.Errorf("主机、用户名、数据库不能为空")
		}
		cfg := *base
		cfg.SetDB(strings.TrimSpace(host.Text), p, strings.TrimSpace(user.Text), pass.Text, strings.TrimSpace(dbname.Text))
		if serviceName != "" {
			cfg.MySQLService = serviceName
		}
		if dumpPath != "" {
			cfg.MysqldumpPath = dumpPath
		}
		return &cfg, nil
	}

	useLocalBtn := widget.NewButton("使用本机 MySQL", func() {
		host.SetText("127.0.0.1")
		if probed != 0 {
			portE.SetText(strconv.Itoa(probed))
		}
		if best != nil {
			serviceName = best.ServiceName
			dumpPath = best.MysqldumpPath
			status.SetText("已套用本机 MySQL 服务：" + best.ServiceName)
		} else {
			status.SetText("未检测到本机 MySQL 服务，请先安装或手动填写。")
		}
	})

	startBtn := widget.NewButton("启动服务", func() {
		if serviceName == "" {
			status.SetText("未检测到 MySQL 服务。")
			return
		}
		if err := mysqlfind.StartService(serviceName); err != nil {
			status.SetText("启动失败：" + err.Error())
			return
		}
		status.SetText("服务已启动。")
	})
	if best == nil || best.Running {
		startBtn.Disable()
	}

	redetectBtn := widget.NewButton("重新检测", func() {
		infos = mysqlfind.Detect()
		probed = mysqlfind.ProbePort()
		best = pickBest(infos)
		if best != nil {
			serviceName = best.ServiceName
			dumpPath = best.MysqldumpPath
			startBtn.Enable()
			if best.Running {
				startBtn.Disable()
			}
		} else {
			startBtn.Disable()
		}
		if probed != 0 {
			portE.SetText(strconv.Itoa(probed))
		}
		detectLabel.SetText(detectText(best, probed))
	})

	testBtn := widget.NewButton("测试连接", func() {
		cfg, err := build()
		if err != nil {
			status.SetText(err.Error())
			return
		}
		status.SetText("正在测试连接...")
		db, err := sqlx.Connect("mysql", cfg.DB.DSN)
		if err != nil {
			status.SetText("连接失败：" + err.Error())
			return
		}
		db.Close()
		status.SetText("连接成功。")
	})

	initBtn := widget.NewButton("初始化并启动", func() {
		cfg, err := build()
		if err != nil {
			status.SetText(err.Error())
			return
		}
		if err := dbsetup.EnsureDatabase(cfg.DB.DSN, cfg.DB.DBName); err != nil {
			status.SetText("创建数据库失败：" + err.Error())
			return
		}
		if dedicated.Checked && cfg.DB.User == "root" {
			pw, err := dbsetup.RandomPassword(24)
			if err == nil {
				if err := dbsetup.EnsureUser(cfg.DB.DSN, "mes_app", pw, cfg.DB.DBName); err == nil {
					cfg.SetDB(cfg.DB.Host, cfg.DB.Port, "mes_app", pw, cfg.DB.DBName)
				}
			}
		}
		db, err := sqlx.Connect("mysql", cfg.DB.DSN)
		if err != nil {
			status.SetText("连接失败：" + err.Error())
			return
		}
		if err := cfg.Save(); err != nil {
			db.Close()
			status.SetText("保存配置失败：" + err.Error())
			return
		}
		onReady(cfg, db)
		w.Close()
	})
	initBtn.Importance = widget.HighImportance

	downloadBtn := widget.NewButton("下载 MySQL 安装器", func() {
		exec.Command("rundll32", "url.dll,FileProtocolHandler", mysqlDownloadURL).Start()
	})

	hint := widget.NewLabel("连接信息将使用 Windows DPAPI 加密后保存在本机。")
	hint.Wrapping = fyne.TextWrapWord

	form := widget.NewForm(
		widget.NewFormItem("主机", host),
		widget.NewFormItem("端口", portE),
		widget.NewFormItem("用户名", user),
		widget.NewFormItem("密码", pass),
		widget.NewFormItem("数据库", dbname),
	)

	content := container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("首次运行配置", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		hint,
		detectLabel,
		container.NewHBox(useLocalBtn, startBtn, redetectBtn, downloadBtn),
		form,
		dedicated,
		container.NewHBox(testBtn, initBtn),
		status,
	))
	w.SetContent(content)
	w.Show()
}

func pickBest(infos []mysqlfind.Info) *mysqlfind.Info {
	var running *mysqlfind.Info
	for i := range infos {
		if infos[i].Running {
			return &infos[i]
		}
		if running == nil {
			running = &infos[i]
		}
	}
	return running
}

func detectText(info *mysqlfind.Info, port int) string {
	if info == nil {
		return "未检测到本机 MySQL 服务。请先安装 MySQL，或手动填写连接信息。"
	}
	state := "已停止"
	if info.Running {
		state = "运行中"
	}
	p := "未知"
	if port != 0 {
		p = strconv.Itoa(port)
	}
	return fmt.Sprintf("已检测到 MySQL 服务：%s（%s，%s），端口 %s", info.ServiceName, info.DisplayName, state, p)
}

func defaultUser(base *config.Config) string {
	if base.DB.User != "" {
		return base.DB.User
	}
	return "root"
}

func defaultDB(base *config.Config) string {
	if base.DB.DBName != "" {
		return base.DB.DBName
	}
	return "appliancedb"
}
