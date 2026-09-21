package ui

import (
	"log"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/service"
)

// TryAutoLogin 尝试用本机记住的凭据自动登录。
func TryAutoLogin(svc *service.Service) (*model.User, bool) {
	username, password, ok := auth.LoadRemembered()
	if !ok {
		return nil, false
	}
	u, err := svc.Login(username, password)
	if err != nil {
		log.Printf("auto login failed: %v", err)
		return nil, false
	}
	auth.SetCurrent(u)
	return u, true
}

// ShowLogin 显示登录窗；若系统尚无任何用户，则引导创建初始管理员。
func ShowLogin(a fyne.App, svc *service.Service, onReady func(*model.User)) {
	n, err := svc.UserCount()
	if err != nil {
		log.Printf("user count failed: %v", err)
		n = 0
	}
	if n == 0 {
		showCreateAdmin(a, svc, onReady)
		return
	}
	showLoginForm(a, svc, onReady)
}

func showLoginForm(a fyne.App, svc *service.Service, onReady func(*model.User)) {
	w := a.NewWindow("RFERP 登录")
	w.SetIcon(AppLogo())
	w.Resize(fyne.NewSize(420, 340))
	w.CenterOnScreen()

	username := widget.NewEntry()
	password := widget.NewPasswordEntry()
	remember := widget.NewCheck("记住登录（下次自动进入）", nil)
	remember.SetChecked(true)

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	if u, _, ok := auth.LoadRemembered(); ok {
		username.SetText(u)
	}

	doLogin := func() {
		u, err := svc.Login(username.Text, password.Text)
		if err != nil {
			status.SetText(err.Error())
			return
		}
		if remember.Checked {
			if err := auth.SaveRemembered(u.Username, password.Text); err != nil {
				log.Printf("save remembered login failed: %v", err)
			}
		} else {
			auth.ClearRemembered()
		}
		auth.SetCurrent(u)
		onReady(u)
		w.Close()
	}

	password.OnSubmitted = func(string) { doLogin() }

	loginBtn := widget.NewButton("登 录", doLogin)
	loginBtn.Importance = widget.HighImportance

	form := widget.NewForm(
		widget.NewFormItem("用户名", username),
		widget.NewFormItem("密码", password),
	)

	content := container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("RFERP 登录", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		form,
		remember,
		loginBtn,
		status,
	))
	w.SetContent(content)
	w.Show()
}

func showCreateAdmin(a fyne.App, svc *service.Service, onReady func(*model.User)) {
	w := a.NewWindow("创建管理员账号")
	w.Resize(fyne.NewSize(460, 400))
	w.CenterOnScreen()

	username := widget.NewEntry()
	display := widget.NewEntry()
	password := widget.NewPasswordEntry()
	confirm := widget.NewPasswordEntry()

	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord

	createBtn := widget.NewButton("创建并进入", func() {
		if strings.TrimSpace(username.Text) == "" {
			status.SetText("请输入用户名")
			return
		}
		if password.Text != confirm.Text {
			status.SetText("两次输入的密码不一致")
			return
		}
		u, err := svc.CreateInitialAdmin(username.Text, password.Text, display.Text)
		if err != nil {
			status.SetText(err.Error())
			return
		}
		auth.SetCurrent(u)
		onReady(u)
		w.Close()
	})
	createBtn.Importance = widget.HighImportance

	form := widget.NewForm(
		widget.NewFormItem("用户名", username),
		widget.NewFormItem("显示名（可选）", display),
		widget.NewFormItem("密码", password),
		widget.NewFormItem("确认密码", confirm),
	)

	content := container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("首次使用：创建管理员账号", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabel("这是本系统第一次运行。请创建一个管理员账号（用户名 + 密码）。"),
		form,
		createBtn,
		status,
	))
	w.SetContent(content)
	w.Show()
}
