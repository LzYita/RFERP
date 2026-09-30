package ui

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"app/internal/config"
	"app/internal/mysqlfind"
	"app/internal/winmsg"
)

// ShowDatabaseUnavailable 在「已配置但连不上数据库」时展示的故障界面。
//
// 与 ShowSetup 的区别很重要：ShowSetup 是首次配置向导，用于还没配置过的新装；
// 而这里配置是好的、只是数据库暂时不可用（MySQL 服务没启动、端口不通、
// 凭据失效等）。若此时弹配置向导，用户会以为配置丢了，甚至可能覆盖掉
// 本来可用的配置。所以这里只说明故障原因，并提供重试与启动服务的入口。
//
// startErr 是尝试启动 MySQL 服务时的错误（nil 表示没尝试过，例如非本机地址）。
// onRetry 应当重新走一遍启动流程。
func ShowDatabaseUnavailable(a fyne.App, cfg *config.Config, reason string, startErr error, onRetry func()) {
	w := a.NewWindow("数据库不可用")

	heading := widget.NewLabelWithStyle("无法连接到数据库", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})

	var detail string
	if cfg != nil {
		detail = fmt.Sprintf("已配置的连接：%s / %s（用户 %s）\n",
			net.JoinHostPort(cfg.DB.Host, strconv.Itoa(cfg.DB.Port)), cfg.DB.DBName, cfg.DB.User)
	}
	detail += "错误详情：" + reason
	if startErr != nil {
		detail += "\n\n启动 MySQL 服务时也失败了：" + startErr.Error()
	}

	// 明确指出配置仍在，避免用户误以为要重新配置
	hint := widget.NewLabelWithStyle(
		"你的配置没有丢失，仍保存在 config.json 中。\n"+
			"这里不需要重新配置，只需让数据库恢复运行后点「重试连接」。",
		fyne.TextAlignLeading, fyne.TextStyle{Italic: true})

	retry := widget.NewButton("重试连接", func() {
		w.Close()
		if onRetry != nil {
			onRetry()
		}
	})
	retry.Importance = widget.HighImportance

	var startBtn *widget.Button
	startBtn = widget.NewButton("启动 MySQL 服务", func() {
		confirmElevate(w, cfg.MySQLService, func() {
			startBtn.Disable()
			retry.Disable()
			go func() {
				err := startConfiguredMySQL(cfg)
				w.Close()
				if err != nil {
					winmsg.Error("启动 MySQL 服务失败", err.Error())
					return
				}
				if onRetry != nil {
					onRetry()
				}
			}()
		})
	})
	if cfg == nil || cfg.MySQLService == "" {
		startBtn.Disable()
	}

	quit := widget.NewButton("退出", func() { a.Quit() })

	content := container.NewVBox(
		heading,
		widget.NewSeparator(),
		widget.NewLabelWithStyle(detail, fyne.TextAlignLeading, fyne.TextStyle{}),
		hint,
		widget.NewSeparator(),
		container.NewHBox(retry, startBtn, quit),
	)

	w.SetContent(container.NewPadded(content))
	w.Resize(fyne.NewSize(580, 360))
	w.CenterOnScreen()
	w.Show()
}

// confirmElevate 在触发 UAC 之前说明来意，让用户知道系统弹窗在做什么、该不该点「是」。
// 用回调而非阻塞返回值：Fyne 的对话框在主线程渲染，阻塞调用方会死锁。
func confirmElevate(parent fyne.Window, service string, proceed func()) {
	content := widget.NewLabelWithStyle(
		"RFERP 需要以管理员权限启动 Windows 服务「"+service+"」。\n\n"+
			"接下来会弹出系统的「用户账户控制」窗口，程序名显示为 RFERP。\n"+
			"确认后请点「是」；点「否」则取消本次操作，RFERP 继续以普通权限运行。\n\n"+
			"提权只用于启动这一个服务，不做其他修改。",
		fyne.TextAlignLeading, fyne.TextStyle{})
	dialog.NewCustomConfirm("需要管理员权限", "继续", "取消", content, func(ok bool) {
		if ok && proceed != nil {
			proceed()
		}
	}, parent).Show()
}

// startConfiguredMySQL 启动配置中指定的 MySQL 服务并等待端口就绪。
//
// 启动 Windows 服务需要管理员权限：未提权时 mysqlfind.StartService 会返回
// NeedsAdmin 错误，这里改用 StartServiceElevated，只把这一条命令通过 UAC
// 提权执行——正常使用时不会出现提权提示。
func startConfiguredMySQL(cfg *config.Config) error {
	if cfg == nil || cfg.MySQLService == "" {
		return errors.New("未配置 MySQL 服务名")
	}
	addr := net.JoinHostPort(cfg.DB.Host, strconv.Itoa(cfg.DB.Port))
	if mySQLPortOpen(addr) {
		return nil
	}

	err := mysqlfind.StartService(cfg.MySQLService)
	if err != nil {
		var se *mysqlfind.StartError
		if errors.As(err, &se) && se.NeedsAdmin {
			if err2 := mysqlfind.StartServiceElevated(cfg.MySQLService); err2 != nil {
				return err2
			}
		} else {
			return err
		}
	}

	for i := 0; i < 30; i++ {
		if mySQLPortOpen(addr) {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("等待 MySQL 就绪超时（%s）", addr)
}

func mySQLPortOpen(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
