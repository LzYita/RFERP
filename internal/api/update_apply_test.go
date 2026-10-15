package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update/apply 的路径闸门（2026-10-13 审计后改）。
//
// 原实现把请求体里的 path 直接交给 update.ApplyAndRestart，而那条链是
// 「从该路径的 zip 里取出任意 .exe → 覆盖程序自身 → 启动它」，且 apply 不验签。
// 于是任何一个已登录会话，只要目标机器上有一个 zip 且知道路径，
// 就能让 ERP 以自己身份运行攻击者的程序。
//
// 现在 apply 只认「下载步骤刚产生并校验过」的那一个，并在应用前再核一次哈希。
// 2026-10-15 起这条路由对所有登录角色开放（不再要求 System.ApplyUpdate），
// 这道路径闸门因此成了**唯一**的路径约束，见 update.go 的函数注释。
//
// ⚠️ 测试**必须**注入假的 applier：真实实现会替换正在运行的测试二进制并启动它。

// newUpdateServer 建服务器，并注入只记录不执行的 applier。
//
// **同时注入一个什么都不做的 exitProcess**：apply 成功之后真实实现会 os.Exit(0)，
// 而这里只关心"applier 被调用了几次、待办文件怎么变"。不注入的话，
// 这些用例会走到真实的退出动作，把整个测试二进制杀掉——go test 表现为中途无声结束。
// 退出行为本身由 update_exit_test.go 单独验证（那里才注入会打信号的假实现）。
func newUpdateServer(t *testing.T) (*Server, *[]string) {
	t.Helper()
	var applied []string
	s := newBackupServer(t, &exportApps{}, WithHostOperations(),
		WithUpdateApplier(func(p string) error {
			applied = append(applied, p)
			return nil
		}),
		WithProcessExit(func() {}))
	return s, &applied
}

// seedZip 造一个"下载产物"，返回路径与它的 sha256。
func seedZip(t *testing.T, body string) (string, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "rferp-update-1.zip")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return p, hex.EncodeToString(sum[:])
}

// 没下载过就 apply —— 必须拒。这是"下载 → 应用"的因果链。
func TestApplyUpdateRequiresPriorDownload(t *testing.T) {
	s, applied := newUpdateServer(t)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code == http.StatusOK {
		t.Fatal("没下载过就允许应用")
	}
	if len(*applied) != 0 {
		t.Errorf("被拒时仍然调用了 applier：%v", *applied)
	}
	// 报错要说清"下一步做什么"，否则用户只会看到一句"缺少下载包路径"
	if !strings.Contains(w.Body.String(), "下载") {
		t.Errorf("拒绝理由没有指路：%s", w.Body.String())
	}
}

// **核心**：客户端传来的 path 一律忽略，只用服务端记住的那个。
//
// 这条是本次修复的实质：修复前，下面这个请求会让 applier 收到攻击者指定的路径。
func TestApplyUpdateIgnoresClientSuppliedPath(t *testing.T) {
	s, applied := newUpdateServer(t)
	tok := issueAdmin(t, s)

	real, sha := seedZip(t, "verified update")
	s.setPendingUpdate(real, sha)

	evil := filepath.Join(t.TempDir(), "attacker.zip")
	if err := os.WriteFile(evil, []byte("evil"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{
		"path":    evil, // 攻击者指认的路径
		"confirm": true,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("应用已记录的更新包 = %d，body=%s", w.Code, w.Body.String())
	}
	if len(*applied) != 1 {
		t.Fatalf("applier 调用 %d 次，期望 1", len(*applied))
	}
	if (*applied)[0] != real {
		t.Errorf("applier 收到的是 %q，期望服务端记住的 %q —— 客户端指认的路径没有被忽略",
			(*applied)[0], real)
	}
}

// 下载与 apply 之间文件被换掉 —— 必须拒。否则"验证过的文件"和"执行的文件"
// 就不是同一个，校验形同虚设。
func TestApplyUpdateRejectsTamperedFile(t *testing.T) {
	s, applied := newUpdateServer(t)
	tok := issueAdmin(t, s)

	p, sha := seedZip(t, "original")
	s.setPendingUpdate(p, sha)

	// 换掉内容（模拟下载与 apply 之间被替换）
	if err := os.WriteFile(p, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code == http.StatusOK {
		t.Fatal("内容与下载时不一致仍然应用了")
	}
	if len(*applied) != 0 {
		t.Errorf("校验失败却仍然调用了 applier：%v", *applied)
	}
	if !strings.Contains(w.Body.String(), "重新下载") {
		t.Errorf("拒绝理由没有指路：%s", w.Body.String())
	}
}

// 被换过之后待办要作废：否则攻击者可以先放一个哈希匹配的旧文件、
// 再换掉，反复试探。
func TestApplyUpdateClearsSlotOnTamper(t *testing.T) {
	s, _ := newUpdateServer(t)
	tok := issueAdmin(t, s)

	p, sha := seedZip(t, "original")
	s.setPendingUpdate(p, sha)
	if err := os.WriteFile(p, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})

	if _, _, none := s.takePendingUpdate(); !none {
		t.Error("校验失败后待办没有作废——用户可以反复重试同一个被换掉的文件")
	}
}

// confirm=false 必须拒，且不碰 applier。
func TestApplyUpdateRequiresConfirm(t *testing.T) {
	s, applied := newUpdateServer(t)
	tok := issueAdmin(t, s)

	p, sha := seedZip(t, "x")
	s.setPendingUpdate(p, sha)

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": false})
	if w.Code != http.StatusBadRequest {
		t.Errorf("未确认 = %d，期望 400", w.Code)
	}
	if len(*applied) != 0 {
		t.Errorf("未确认却调用了 applier：%v", *applied)
	}
}

// 成功之后作废待办：更新包只能应用一次。
func TestApplyUpdateConsumesSlotOnce(t *testing.T) {
	s, applied := newUpdateServer(t)
	tok := issueAdmin(t, s)

	p, sha := seedZip(t, "x")
	s.setPendingUpdate(p, sha)

	if w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true}); w.Code != http.StatusOK {
		t.Fatalf("第一次应用 = %d", w.Code)
	}
	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code == http.StatusOK {
		t.Error("同一个更新包可以被应用两次")
	}
	if len(*applied) != 1 {
		t.Errorf("applier 调用 %d 次，期望 1", len(*applied))
	}
}

// applier 失败时待办**保留**：文件还在，用户可以直接重试，不必重新下载。
func TestApplyUpdateKeepsSlotWhenApplierFails(t *testing.T) {
	var applied []string
	s := newBackupServer(t, &exportApps{}, WithHostOperations(),
		WithUpdateApplier(func(p string) error {
			applied = append(applied, p)
			return errors.New("安装目录不可写")
		}))
	tok := issueAdmin(t, s)

	p, sha := seedZip(t, "x")
	s.setPendingUpdate(p, sha)

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code != http.StatusInternalServerError {
		t.Errorf("applier 失败 = %d，期望 500", w.Code)
	}
	if _, _, none := s.takePendingUpdate(); none {
		t.Error("applier 失败后待办被清掉了——用户得重新下载，而文件其实还在")
	}
}

// 文件不存在（被清理掉了）要给出可行动的提示，不能 500。
func TestApplyUpdateMissingFileGuidesToRedownload(t *testing.T) {
	s, applied := newUpdateServer(t)
	tok := issueAdmin(t, s)

	p, sha := seedZip(t, "x")
	s.setPendingUpdate(p, sha)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code == http.StatusOK {
		t.Fatal("文件不存在却应用成功")
	}
	if len(*applied) != 0 {
		t.Errorf("文件不存在却调用了 applier：%v", *applied)
	}
	if !strings.Contains(w.Body.String(), "重新下载") {
		t.Errorf("理由没有指路：%s", w.Body.String())
	}
}
