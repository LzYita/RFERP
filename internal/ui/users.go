package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"app/internal/auth"
	"app/internal/model"
	"app/internal/service"
)

type UsersScreen struct {
	svc      *service.Service
	window   fyne.Window
	data     []model.User
	table    *widget.Table
	label    *widget.Label
	selected int
}

func NewUsersScreen(svc *service.Service, w fyne.Window) *UsersScreen {
	return &UsersScreen{svc: svc, window: w, selected: -1}
}

func (s *UsersScreen) Build() fyne.CanvasObject {
	bar := container.NewHBox(
		withImportance(widget.NewButtonWithIcon("新增用户", theme.ContentAddIcon(), s.add), widget.HighImportance),
		widget.NewButtonWithIcon("编辑", theme.DocumentCreateIcon(), s.edit),
		widget.NewButtonWithIcon("重置密码", theme.LoginIcon(), s.resetPassword),
		withImportance(widget.NewButtonWithIcon("删除", theme.DeleteIcon(), s.delete), widget.DangerImportance),
		widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), s.Refresh),
	)

	s.label = widget.NewLabel("共 0 个用户")

	s.selected = -1
	s.table = widget.NewTable(
		func() (int, int) { return len(s.data) + 1, 6 },
		makeCellTmpl,
		func(tci widget.TableCellID, o fyne.CanvasObject) {
			sel := tci.Row == s.selected && tci.Row > 0
			if tci.Row == 0 {
				headers := []string{"用户名", "显示名", "角色", "状态", "最后登录", "选择"}
				updateCell(o, headers[tci.Col], true, headerColor)
				return
			}
			idx := tci.Row - 1
			if idx >= len(s.data) {
				return
			}
			u := s.data[idx]
			bg := dataRowBG(idx, sel)
			switch tci.Col {
			case 0:
				updateCell(o, u.Username, true, bg)
			case 1:
				updateCell(o, nullStr(u.DisplayName), true, bg)
			case 2:
				updateCell(o, auth.RoleFromString(u.Role).Label(), true, bg)
			case 3:
				status := "启用"
				if u.Status != 1 {
					status = "停用"
				}
				updateCell(o, status, true, bg)
			case 4:
				t := "—"
				if u.LastLoginAt != nil {
					t = u.LastLoginAt.Format("01-02 15:04")
				}
				updateCell(o, t, true, bg)
			case 5:
				if sel {
					updateCell(o, "☑", true, bg)
				} else {
					updateCell(o, "☐", true, bg)
				}
			}
		},
	)
	var selGuard bool
	s.table.OnSelected = func(id widget.TableCellID) {
		if selGuard {
			selGuard = false
			return
		}
		if id.Row == 0 {
			return
		}
		if s.selected == id.Row {
			s.selected = -1
		} else {
			s.selected = id.Row
		}
		s.table.Refresh()
		selGuard = true
		s.table.Select(widget.TableCellID{Row: -1, Col: -1})
	}
	s.table.SetColumnWidth(0, 160)
	s.table.SetColumnWidth(1, 180)
	s.table.SetColumnWidth(2, 120)
	s.table.SetColumnWidth(3, 100)
	s.table.SetColumnWidth(4, 200)
	s.table.SetColumnWidth(5, 90)

	s.Refresh()
	return container.NewBorder(bar, s.label, nil, nil, s.table)
}

func (s *UsersScreen) Refresh() {
	list, err := s.svc.ListUsers()
	if err != nil {
		showError(s.window, "查询用户失败", err)
		return
	}
	s.data = list
	if s.label != nil {
		s.label.SetText(fmt.Sprintf("共 %d 个用户", len(s.data)))
	}
	if s.table != nil {
		s.selected = -1
		s.table.Refresh()
	}
}

func (s *UsersScreen) selectedUser() (*model.User, bool) {
	if s.selected <= 0 || s.selected-1 >= len(s.data) {
		dialog.ShowInformation("提示", "请先选择一行用户", s.window)
		return nil, false
	}
	return &s.data[s.selected-1], true
}

func roleLabels() []string {
	roles := auth.Roles()
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, r.Label())
	}
	return out
}

func roleKeyFromLabel(label string) string {
	for _, r := range auth.Roles() {
		if r.Label() == label {
			return string(r)
		}
	}
	return string(auth.RoleViewer)
}

func (s *UsersScreen) add() {
	username := widget.NewEntry()
	display := widget.NewEntry()
	password := widget.NewPasswordEntry()
	confirm := widget.NewPasswordEntry()
	role := widget.NewSelect(roleLabels(), nil)
	role.SetSelectedIndex(len(auth.Roles()) - 1)

	items := []*widget.FormItem{
		widget.NewFormItem("用户名", username),
		widget.NewFormItem("显示名（可选）", display),
		widget.NewFormItem("密码", password),
		widget.NewFormItem("确认密码", confirm),
		widget.NewFormItem("角色", role),
	}
	d := dialog.NewForm("新增用户", "确定", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		if strings.TrimSpace(username.Text) == "" {
			dialog.ShowInformation("提示", "请输入用户名", s.window)
			return
		}
		if password.Text != confirm.Text {
			dialog.ShowInformation("提示", "两次输入的密码不一致", s.window)
			return
		}
		if _, err := s.svc.CreateUser(username.Text, password.Text, display.Text, roleKeyFromLabel(role.Selected)); err != nil {
			showError(s.window, "新增失败", err)
			return
		}
		s.Refresh()
	}, s.window)
	d.Resize(fyne.NewSize(480, 440))
	d.Show()
}

func (s *UsersScreen) edit() {
	u, ok := s.selectedUser()
	if !ok {
		return
	}
	display := widget.NewEntry()
	display.SetText(nullStr(u.DisplayName))
	role := widget.NewSelect(roleLabels(), nil)
	role.SetSelected(auth.RoleFromString(u.Role).Label())
	status := widget.NewSelect([]string{"启用", "停用"}, nil)
	if u.Status == 1 {
		status.SetSelected("启用")
	} else {
		status.SetSelected("停用")
	}

	items := []*widget.FormItem{
		widget.NewFormItem("用户名", widget.NewLabel(u.Username)),
		widget.NewFormItem("显示名", display),
		widget.NewFormItem("角色", role),
		widget.NewFormItem("状态", status),
	}
	d := dialog.NewForm("编辑用户", "确定", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		st := 1
		if status.Selected == "停用" {
			st = 0
		}
		if cur := auth.Current(); cur != nil && cur.ID == u.ID && st == 0 {
			dialog.ShowInformation("提示", "不能停用当前登录账号", s.window)
			return
		}
		if err := s.svc.UpdateUser(u.ID, display.Text, roleKeyFromLabel(role.Selected), st); err != nil {
			showError(s.window, "保存失败", err)
			return
		}
		s.Refresh()
	}, s.window)
	d.Resize(fyne.NewSize(480, 380))
	d.Show()
}

func (s *UsersScreen) resetPassword() {
	u, ok := s.selectedUser()
	if !ok {
		return
	}
	password := widget.NewPasswordEntry()
	confirm := widget.NewPasswordEntry()
	items := []*widget.FormItem{
		widget.NewFormItem("新密码", password),
		widget.NewFormItem("确认密码", confirm),
	}
	d := dialog.NewForm("重置密码 - "+u.Username, "确定", "取消", items, func(ok bool) {
		if !ok {
			return
		}
		if password.Text != confirm.Text {
			dialog.ShowInformation("提示", "两次输入的密码不一致", s.window)
			return
		}
		if err := s.svc.ResetPassword(u.ID, password.Text); err != nil {
			showError(s.window, "重置失败", err)
			return
		}
		dialog.ShowInformation("完成", "密码已重置", s.window)
	}, s.window)
	d.Resize(fyne.NewSize(460, 300))
	d.Show()
}

func (s *UsersScreen) delete() {
	u, ok := s.selectedUser()
	if !ok {
		return
	}
	if cur := auth.Current(); cur != nil && cur.ID == u.ID {
		dialog.ShowInformation("提示", "不能删除当前登录账号", s.window)
		return
	}
	dialog.NewConfirm("确认删除", fmt.Sprintf("确定删除用户 %s？", u.Username), func(ok bool) {
		if !ok {
			return
		}
		if err := s.svc.DeleteUser(u.ID); err != nil {
			showError(s.window, "删除失败", err)
			return
		}
		s.selected = -1
		s.Refresh()
	}, s.window).Show()
}
