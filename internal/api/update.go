package api

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
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
	writeJSON(w, http.StatusOK, map[string]any{"path": zipPath, "version": m.Version})
}

// handleApplyUpdate 解压替换 exe 并重启。
//
// 单独一步是刻意的：前面两步都不碰用户正在运行的程序，只有到这里才动。
// 调用方必须已经拿到用户确认。
func (s *Server) handleApplyUpdate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path    string `json:"path"`
		Confirm bool   `json:"confirm"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	// 没有显式确认就不替换。这条不能省——它是"下载"和"动手"之间唯一的闸门。
	if !in.Confirm {
		writeErr(w, http.StatusBadRequest, "需要显式确认才能替换程序文件")
		return
	}
	if in.Path == "" {
		writeErr(w, http.StatusBadRequest, "缺少下载包路径")
		return
	}
	if err := update.ApplyAndRestart(in.Path); err != nil {
		writeErr(w, http.StatusInternalServerError, "更新失败: "+err.Error())
		return
	}
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
