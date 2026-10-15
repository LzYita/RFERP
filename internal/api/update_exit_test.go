package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

// apply 之后必须退出旧进程（Round 8 先决项⑥，2026-10-15 实施①时查出）。
//
// 事故：`update.ApplyAndRestart` 只负责启动新进程，文档里明写"调用方随后应退出"。
// 原生弹框那条路一直有 os.Exit(0)（Fyne 版同位置也有），只有这个 HTTP 端点漏了。
// 后果不是报错，而是**静默无效**：旧进程继续占着单实例锁，新进程带着
// RFERP_UPDATE_RESTART=1 等满 20 秒后弹"已在运行"并自己退出——用户点了安装，
// exe 却没被换掉，界面上还显示"restarting"。
//
// 这组测试断言三件事：
//  1. apply 成功后确实安排了退出；
//  2. 退出**不是立刻**发生的（响应要先送达）；
//  3. 退出被注入替换，绝不会把跑测试的进程杀掉。
var errApplyBoom = errors.New("安装目录不可写")

func newApplyServer(t *testing.T) (*Server, *int, chan struct{}) {
	t.Helper()
	var applied int
	exited := make(chan struct{}, 1)
	s := New(&sessionApps{},
		WithUpdateApplier(func(zipPath string) error {
			applied++
			return nil
		}),
		WithProcessExit(func() { exited <- struct{}{} }))
	p, sha := seedZip(t, "rest")
	s.setPendingUpdate(p, sha)
	return s, &applied, exited
}

func TestApplySchedulesProcessExit(t *testing.T) {
	s, applied, exited := newApplyServer(t)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code != http.StatusOK {
		t.Fatalf("apply = %d，期望 200", w.Code)
	}
	if *applied != 1 {
		t.Fatalf("applier 调用 %d 次，期望 1", *applied)
	}
	// 响应内容仍是"正在重启"——前端据此刷新/提示
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out["status"] != "restarting" {
		t.Errorf("响应 status = %v，期望 restarting", out["status"])
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("apply 成功后没有退出旧进程：更新会静默无效（先决项⑥）")
	}
}

// 退出必须延后：立刻 os.Exit 的话响应还没出去，前端看到的是网络错误而不是
// "restarting"，白白让人以为失败。这条把"延后"钉住——改回立刻退出会红。
func TestApplyExitIsDeferredNotImmediate(t *testing.T) {
	s, _, exited := newApplyServer(t)
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code != http.StatusOK {
		t.Fatalf("apply = %d，期望 200", w.Code)
	}
	// 请求已经返回，此刻退出还没发生
	select {
	case <-exited:
		t.Fatal("退出发生在响应之前：客户端会看到网络错误而不是 restarting")
	default:
	}
	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("延后退出没有发生")
	}
}

// apply **失败**时不能退出：程序还好好地跑着，用户可以直接重试（文件还在，
// 不必重新下载）。这里失败会杀掉进程，属于最严重的一类回归。
func TestApplyFailureDoesNotExit(t *testing.T) {
	exited := make(chan struct{}, 1)
	s := New(&sessionApps{},
		WithUpdateApplier(func(zipPath string) error { return errApplyBoom }),
		WithProcessExit(func() { exited <- struct{}{} }))
	tok := issueAdmin(t, s)
	p, sha := seedZip(t, "boom")
	s.setPendingUpdate(p, sha)

	w := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("apply 失败时 = %d，期望 500", w.Code)
	}
	select {
	case <-exited:
		t.Fatal("apply 失败却退出了进程：用户会丢掉一个还能重试的状态")
	case <-time.After(exitAfterResponseDelay * 3):
	}
}

// 没带 confirm / 尚未下载 / 哈希不符这三条前置失败路径同样不许退出。
func TestApplyRejectedPathsDoNotExit(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, s *Server) map[string]any
	}{
		{"缺 confirm", func(t *testing.T, s *Server) map[string]any {
			p, sha := seedZip(t, "x")
			s.setPendingUpdate(p, sha)
			return map[string]any{}
		}},
		{"尚未下载", func(t *testing.T, s *Server) map[string]any {
			return map[string]any{"confirm": true}
		}},
		{"内容被换过", func(t *testing.T, s *Server) map[string]any {
			p, sha := seedZip(t, "orig")
			s.setPendingUpdate(p, sha)
			if err := os.WriteFile(p, []byte("tampered"), 0o600); err != nil {
				t.Fatal(err)
			}
			return map[string]any{"confirm": true}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exited := make(chan struct{}, 1)
			s := New(&sessionApps{},
				WithUpdateApplier(func(string) error { return nil }),
				WithProcessExit(func() { exited <- struct{}{} }))
			tok := issueAdmin(t, s)
			body := c.setup(t, s)

			w := doJSON(t, s, "POST", "/api/update/apply", tok, body)
			if w.Code == http.StatusOK {
				t.Fatalf("前置校验没过却返回 200：%s", c.name)
			}
			select {
			case <-exited:
				t.Fatalf("%s：校验没过却退出了进程", c.name)
			case <-time.After(exitAfterResponseDelay * 3):
			}
		})
	}
}

// 默认实现必须是 os.Exit —— 用 WithProcessExit 把它换成假的之后，
// 真实路径仍需保证"有人会退出"，否则漏了 Option 就静默变成不退出。
func TestDefaultProcessExitIsWired(t *testing.T) {
	s := New(&sessionApps{})
	if s.exitProcess == nil {
		t.Fatal("exitProcess 为 nil：漏传 Option 时 apply 成功将不退出，缺陷⑥会复现")
	}
	// 不能真的调用它（会把测试进程杀掉）。用函数名确认它是默认实现而不是注入的假实现。
	name := runtime.FuncForPC(reflect.ValueOf(s.exitProcess).Pointer()).Name()
	if !strings.Contains(name, "exitProcessNow") {
		t.Errorf("默认 exitProcess = %q，期望 exitProcessNow", name)
	}
}
