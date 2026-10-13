package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"app/internal/update"
	"app/internal/winmsg"
)

// ---- 软件更新 ----
//
// 后端能力全在 internal/update（ed25519 签名校验、版本比较、下载、解压、
// 替换、重启），这里只把它接成端点，供设置屏（Round 7）与启动检查使用。
//
// 两点不能含糊：
//
//  1. **未配置清单地址就完全关闭**。cfg.UpdateURL 为空是默认状态（本地部署
//     不需要联网检查），此时不能发任何网络请求，也不能弹任何提示。
//
//  2. **更新必须用户确认后才会下载并替换 exe**。静默自动更新会直接覆盖
//     用户正在运行的程序——Fyne 版是 dialog.ShowConfirm 交互式的，这里沿用
//     同样的语义：端点只负责"查"和"下载"，"应用"要单独一步且带显式确认。

type updateChecker struct {
	once sync.Once
	url  string
	cur  string
	err  error
	c    *update.Checker
}

func (s *Server) updateURL() string { return s.updater.url }

func (s *Server) checkerFor(url string) (*update.Checker, error) {
	s.updater.once.Do(func() {
		s.updater.c, s.updater.err = update.NewChecker(url, s.updater.cur)
	})
	return s.updater.c, s.updater.err
}

// handleCheckUpdate 查询是否有新版本。不下载、不改任何东西。
func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	url := s.updater.url
	if url == "" {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	checker, err := s.checkerFor(url)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "更新检查不可用: "+err.Error())
		return
	}
	m, err := checker.Check()
	if err != nil {
		writeErr(w, http.StatusBadGateway, "无法获取更新信息: "+err.Error())
		return
	}
	out := map[string]any{
		"enabled":        true,
		"currentVersion": s.updater.cur,
		"hasUpdate":      m != nil,
	}
	if m != nil {
		out["version"] = m.Version
		out["notes"] = m.Notes
		out["size"] = m.Size
		out["publishedAt"] = m.PublishedAt
		out["channel"] = m.Channel
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDownloadUpdate 下载（不应用）。前端拿到进度后再单独请求 apply。
func (s *Server) handleDownloadUpdate(w http.ResponseWriter, r *http.Request) {
	url := s.updater.url
	if url == "" {
		writeErr(w, http.StatusBadRequest, "未配置更新地址")
		return
	}
	checker, err := s.checkerFor(url)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "更新检查不可用: "+err.Error())
		return
	}
	m, err := checker.Check()
	if err != nil || m == nil {
		writeErr(w, http.StatusBadRequest, "没有可下载的新版本")
		return
	}
	zipPath, err := checker.Download(m, os.TempDir(), func(done, total int64) {
		// 进度只写日志：HTTP 长轮询进度条要求客户端保持连接，
		// 与「下载完再 apply」的两步式模型不合。
		if total > 0 && done%(m.Size/20+1) == 0 {
			log.Printf("update download: %d/%d bytes", done, total)
		}
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "下载失败: "+err.Error())
		return
	}
	// 记住「刚下载并通过校验的那一个」。apply 只认它，客户端不再有机会指认路径。
	// sha 用清单里的值：downloadOnce 已经核对过文件与它一致。
	s.setPendingUpdate(zipPath, m.SHA256)
	writeJSON(w, http.StatusOK, map[string]any{"path": zipPath, "version": m.Version})
}

// setPendingUpdate 记录待应用的更新包。
func (s *Server) setPendingUpdate(path, sha string) {
	s.pendingUpdate.mu.Lock()
	defer s.pendingUpdate.mu.Unlock()
	s.pendingUpdate.path = path
	s.pendingUpdate.sha = sha
}

// takePendingUpdate 读取待应用的更新包。none 为 true 表示没有下载过。
func (s *Server) takePendingUpdate() (path, sha string, none bool) {
	s.pendingUpdate.mu.Lock()
	defer s.pendingUpdate.mu.Unlock()
	return s.pendingUpdate.path, s.pendingUpdate.sha, s.pendingUpdate.path == ""
}

// clearPendingUpdate 清掉待应用的更新包（应用成功后调用）。
func (s *Server) clearPendingUpdate() {
	s.pendingUpdate.mu.Lock()
	defer s.pendingUpdate.mu.Unlock()
	s.pendingUpdate.path = ""
	s.pendingUpdate.sha = ""
}

// handleApplyUpdate 解压替换 exe 并重启。
//
// **只能用「下载步骤刚产生并校验过」的那个更新包**：请求体里的 path 一律忽略。
//
// 这一条是 2026-10-13 审计后改的。此前它直接拿请求体的 path 交给
// update.ApplyAndRestart，而那条链是「从该路径的 zip 里取出任意 .exe →
// 覆盖程序自身 → 启动它」，且 apply 阶段不验签。于是任何持有 System.ApplyUpdate
// 的会话，只要目标机器上存在一个 zip 且知道路径，就能让 ERP 以自己身份运行
// 攻击者的程序（随之可读 config.json、连数据库、读写全部备份）。
//
// 原设计里「下载时验过 SHA-256」是对的，但**验证绑在下载产生的临时文件上，
// 与 apply 传进来的路径毫无关联** —— 等于把校验和"将执行的文件"割开了。
// 现在两者是同一个对象：路径由服务端记住，apply 前再核一次哈希，
// 连"下载与 apply 之间被换文件"这个窗口也一并关掉。
func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		// Path 保留仅为线协议兼容，**不再使用**。前端仍会回传下载得到的路径，
		// 但服务端只认自己记住的那一个。
		Path    string `json:"path"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	// 显式确认仍然要。它是「下载」和「动手」之间给**用户**看的闸门；
	// 真正拦住路径替换的是下面那条因果链，不是这个布尔值。
	if !in.Confirm {
		writeErr(w, http.StatusBadRequest, "需要显式确认才能替换程序文件")
		return
	}

	zipPath, wantSHA, none := s.takePendingUpdate()
	if none {
		writeErr(w, http.StatusBadRequest, "尚未下载更新包，请先下载")
		return
	}
	// apply 之前再核对一次哈希：验证过的文件与即将执行的文件必须是同一个。
	gotSHA, err := update.FileSHA256(zipPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "更新包已不可读取，请重新下载")
		return
	}
	if !strings.EqualFold(gotSHA, wantSHA) {
		// 文件被换过了。作废这个待办，逼用户重新走一次下载（下载会重新验签）。
		s.clearPendingUpdate()
		writeErr(w, http.StatusBadRequest, "更新包内容与下载时不一致，已拒绝应用，请重新下载")
		return
	}

	if err := s.applyUpdate(zipPath); err != nil {
		// 失败**不清**待办：文件还在，用户可以直接重试，不必重新下载。
		writeErr(w, http.StatusInternalServerError, "更新失败: "+err.Error())
		return
	}
	s.clearPendingUpdate()
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})
}

// startUpdateCheck 在启动后异步查一次，有新版就用原生对话框提示。
//
// 与 Fyne 版一致：延时几秒再查，避免和首屏/数据库初始化抢资源。
// 查不到、没配置、用户不点确认——都安静退出，不打扰。
func startUpdateCheck(url, current string) {
	if url == "" {
		return
	}
	go func() {
		time.Sleep(3 * time.Second)
		checker, err := update.NewChecker(url, current)
		if err != nil {
			log.Printf("update check disabled: %v", err)
			return
		}
		m, err := checker.Check()
		if err != nil {
			log.Printf("update check failed: %v", err)
			return
		}
		if m == nil {
			return
		}
		log.Printf("update available: %s (current %s)", m.Version, current)
		msg := "发现新版本 " + m.Version + "（当前 " + current + "）"
		if m.Notes != "" {
			msg += "\n\n" + m.Notes
		}
		msg += "\n\n是否立即更新？"
		if !winmsg.Confirm("软件更新", msg) {
			return
		}
		zipPath, err := checker.Download(m, os.TempDir(), nil)
		if err != nil {
			winmsg.Error("软件更新失败", err.Error())
			return
		}
		if err := update.ApplyAndRestart(zipPath); err != nil {
			winmsg.Error("软件更新失败", err.Error())
		}
	}()
}
