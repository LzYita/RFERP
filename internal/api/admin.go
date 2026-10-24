package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"app/internal/paths"
	"app/internal/usecase"
)

func (s *Server) handleListUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.apps.ListUsers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleUserCount(w http.ResponseWriter, _ *http.Request) {
	n, err := s.apps.UserCount()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var in usecase.CreateUserInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, err := s.apps.CreateUser(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var in usecase.UpdateUserInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	// 改角色前先记下旧角色：权限点存在会话快照里，不主动踢下线的话，
	// 对方当前会话会继续按旧角色放行，管理员会以为没改成功。
	// Applications 没有单查用户的入口，这里从列表里找。
	prevRole := ""
	if users, err := s.apps.ListUsers(); err == nil {
		for _, u := range users {
			if u.ID == in.ID {
				prevRole = u.Role
				break
			}
		}
	}
	if err := s.apps.UpdateUser(in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Status == 2 || (prevRole != "" && prevRole != in.Role) {
		if n := s.RevokeUserSessions(in.ID); n > 0 {
			log.Printf("access changed for user %d (%s -> %s): 强制下线 %d 个会话",
				in.ID, prevRole, in.Role, n)
		}
		// 凭证里存的是角色快照，不踢就会按旧角色继续放行。
		s.revokeRememberForUser(in.ID)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var in usecase.ResetPasswordInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.ResetPassword(in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 改密码后旧凭证必须失效，否则「记住登录」会绕过新密码。
	s.RevokeUserSessions(in.ID)
	s.revokeRememberForUser(in.ID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleChangePassword 只允许改自己的密码（登录即可，不需要 users/write）；
// 管理员重置他人密码走 /api/users/reset-password。
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var in usecase.ChangePasswordInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, ok := s.userFromRequest(r)
	if !ok || u == nil {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if in.ID != u.ID {
		writeErr(w, http.StatusForbidden, "can only change your own password")
		return
	}
	if err := s.apps.ChangePassword(in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	// 旧凭证里不含新密码，作废它才算真的改了密码。
	s.RevokeUserSessions(in.ID)
	s.revokeRememberForUser(in.ID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.apps.DeleteUser(id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.RevokeUserSessions(id)
	s.revokeRememberForUser(id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListProducts(w http.ResponseWriter, _ *http.Request) {
	list, err := s.apps.ListProducts()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateProduct(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeProductWrite(w, r)
	if !ok {
		return
	}
	u, _ := s.userFromRequest(r)
	name := operatorName(u)
	in.Operator = &name

	out, err := s.apps.CreateProduct(in.toProductForCreate())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// 导出目录下的分类子目录名。与旧版一致（ui/backup.go:313/317）。
//
// 这不是洁癖：旧版的用户已经知道东西在 导出\all_data\ 与 导出\audit_log\，
// Web 版一开始直接扔在 导出\ 根下，于是每次导出多出一个
// `20261013_160107-1666890059` 这种临时目录名 —— 用户找不到，也不敢删。
const (
	exportSubdirAllData  = "all_data"
	exportSubdirAuditLog = "audit_log"
)

func (s *Server) handleExportAll(w http.ResponseWriter, _ *http.Request) {
	// 恢复到旧版的分组目录，而不是 os.MkdirTemp 在 导出\ 根下造随机名目录。
	files, dir, err := s.apps.ExportAllDataCSV(filepath.Join(paths.ExportDir(), exportSubdirAllData))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 回**文件名**而不是完整路径：界面要列出文件，用不着每条都带一遍目录，
	// 而把绝对路径逐条回给客户端是这个端点从 Round 5 起就在避免的事。
	names := make([]string, 0, len(files))
	for _, p := range files {
		names = append(names, filepath.Base(p))
	}
	sort.Strings(names)
	// dir 是服务器端路径，但界面上必须显示它——否则用户不知道东西去哪了。
	// 与 handleDataDir 同理：本端点已鉴权且要求备份模块权限，是"展示给操作者本人"。
	writeJSON(w, http.StatusOK, map[string]any{"files": names, "dir": dir})
}

// explorerFolderOpener 是默认的打开方式：调起 Windows 资源管理器。
//
// 之所以做成可注入（Server.openFolder）：这个动作**有真实副作用**——
// 在开发者的桌面上弹出一个窗口。测试若走真实路径，每跑一遍就开一个窗口，
// 而且在 CI 上还会去启动一个 GUI 程序。所以测试注入一个只记录不执行的实现，
// 顺带还能断言"传给启动器的到底是哪个目录"，比只断言 200 更强。
func explorerFolderOpener(dir string) error {
	// explorer.exe 选中该目录而不是打开它的父目录
	return exec.Command("explorer.exe", dir).Start()
}

// handleOpenExportFolder 在资源管理器里打开导出目录。
//
// 本机操作：它启动的是**这台机器上**的程序。阶段 B/C 的远程客户端点了只会
// 在用户自己电脑上打开一个不存在的路径，所以走 hostOperations 闸门。
func (s *Server) handleOpenExportFolder(w http.ResponseWriter, r *http.Request) {
	if !s.requireHostOperations(w, "打开导出文件夹") {
		return
	}
	var body struct {
		Dir string `json:"dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	dir, err := paths.ResolveExistingUnder(paths.ExportDir(), strings.TrimSpace(body.Dir))
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "导出目录不存在")
			return
		}
		writeErr(w, http.StatusBadRequest, "只能打开本应用的导出目录")
		return
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		writeErr(w, http.StatusNotFound, "导出目录不存在")
		return
	}
	// explorer.exe 选中该目录而不是打开它的父目录
	if err := s.openFolder(dir); err != nil {
		writeErr(w, http.StatusInternalServerError, "无法打开文件夹："+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"dir": dir})
}
