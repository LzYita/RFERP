package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// 前置检查必须在替换 exe **之前**：升级前备份需要本机 mysqldump，缺工具时
// 先把 exe 换掉，用户能用的旧版本就没了，新版本却在第一次启动时中止
// （2026-11-10 的事故形态）。
func TestApplyUpdatePreflightBlocksReplacement(t *testing.T) {
	var applied int
	s := newBackupServer(t, &exportApps{}, WithHostOperations(),
		WithUpdateApplier(func(string) error {
			applied++
			return nil
		}),
		WithProcessExit(func() {}),
		WithUpdatePreflight(func() error {
			return errors.New("未找到 mysqldump：请安装 MySQL 客户端")
		}))
	tok := issueAdmin(t, s)
	p, sha := seedZip(t, "rest")
	s.setPendingUpdate(p, sha)

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code == http.StatusOK {
		t.Fatal("前置检查失败仍然允许替换 exe")
	}
	if applied != 0 {
		t.Errorf("前置检查失败后 applier 被调用 %d 次", applied)
	}
	if !strings.Contains(w.Body.String(), "mysqldump") {
		t.Errorf("拒绝理由没有说明缺什么: %s", w.Body.String())
	}
	// 待办不是为了这一次失败而准备的：用户修好工具后应当能直接重试。
	if _, _, none := s.takePendingUpdate(); none {
		t.Error("前置检查失败把待办清掉了，用户必须重新下载")
	}
}

// 检查通过时，顺序必须是「先检查、后替换」。
func TestApplyUpdateRunsPreflightBeforeApply(t *testing.T) {
	var order []string
	s := newBackupServer(t, &exportApps{}, WithHostOperations(),
		WithUpdateApplier(func(string) error {
			order = append(order, "apply")
			return nil
		}),
		WithProcessExit(func() {}),
		WithUpdatePreflight(func() error {
			order = append(order, "preflight")
			return nil
		}))
	tok := issueAdmin(t, s)
	p, sha := seedZip(t, "rest")
	s.setPendingUpdate(p, sha)

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code != http.StatusOK {
		t.Fatalf("apply = %d，期望 200：%s", w.Code, w.Body.String())
	}
	want := []string{"preflight", "apply"}
	if len(order) != len(want) || order[0] != want[0] || order[1] != want[1] {
		t.Fatalf("调用顺序 = %v，期望 %v", order, want)
	}
}
