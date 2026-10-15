package api

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// 手动「查询最新更新」→ 弹原生确认框 这条路（2026-10-15）。
//
// 它存在的理由是设置屏原先只能"看到"有新版：查到的结果只写一行文字，把用户指向
// "启动提示"，而启动提示未必在他眼前（也可能已经被他关掉）。
//
// 这组测试守三件事：
//  1. 未登录不行（弹框会替换程序文件，匿名请求不该能触发）；
//  2. 没配置更新源时说清楚是没配置，而不是弹一个查不到东西的框；
//  3. 同一时刻只允许一条更新流程（启动检查与手动查询可能同时到达）。
//
// 下面**没有**"弹了框之后会怎样"的测试：那一步会调 winmsg.Confirm，在 Windows 上
// 真弹一个模态框并阻塞，测试进程会被挂住。真弹框的验证靠实机演练（Round 7 关口
// 用的就是那套：把清单指向更高版本，然后人工点"是"）。
func TestUpdatePromptRejectsAnonymous(t *testing.T) {
	s := New(&sessionApps{}, WithUpdate("https://example.invalid/releases.json", "1.3.0"))
	w := postJSON(t, s, "POST", "/api/update/prompt", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("匿名对 /api/update/prompt 返回 %d，期望 401", w.Code)
	}
}

func TestUpdatePromptNeedsConfiguredSource(t *testing.T) {
	s := New(&sessionApps{})
	tok := issueAs(t, s, "viewer")
	w := postJSON(t, s, "POST", "/api/update/prompt", tok, nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("未配置更新源时 = %d，期望 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "未配置更新地址") {
		t.Errorf("响应 %q 里没有说明是未配置更新地址", w.Body.String())
	}
}

// 配置了更新源就应当受理（202），并把流程交给后台 runner——对话框要等人点，
// 下载可能几分钟，HTTP 请求不能挂在那里。
//
// runner 用注入的假实现：真实流程会去连清单地址（连不上还会退到真实的 GitHub
// 发布页，那里可能真有更高的正式版本），在 Windows 上还会弹模态框等人点。
// 这里断言的是「受理了、并且把正确的地址与当前版本交给了流程」。
func TestUpdatePromptAcceptedWhenConfigured(t *testing.T) {
	var gotURL, gotCurrent string
	done := make(chan struct{})
	s := New(&sessionApps{},
		WithUpdate("https://example.invalid/releases.json", "1.3.0"),
		WithUpdatePromptRunner(func(url, current string) {
			// 真实流程用 defer promptGate.release() 归还执行权；假实现必须照样做，
			// 否则 promptGate 这个包级状态会被本用例永久占住。
			defer promptGate.release()
			gotURL, gotCurrent = url, current
			close(done)
		}))
	tok := issueAs(t, s, "viewer")

	w := postJSON(t, s, "POST", "/api/update/prompt", tok, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("已配置更新源时 = %d，期望 202（立即受理，后台弹框）", w.Code)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("后台流程没有被调用")
	}
	if gotURL != "https://example.invalid/releases.json" || gotCurrent != "1.3.0" {
		t.Errorf("流程收到 url=%q current=%q，期望与配置一致", gotURL, gotCurrent)
	}
}

// 执行权：一次只允许一条流程。启动检查在 3 秒后弹框，手动查询随时可能也来一次；
// 两个框叠着、两次下载各写各的待应用文件，pendingUpdate 只认最后一次。
func TestPromptGateIsSingleFlight(t *testing.T) {
	var g updatePromptGate
	if !g.acquire() {
		t.Fatal("空闲时抢占应当成功")
	}
	if g.acquire() {
		t.Fatal("已被占用时第二次抢占竟然成功了")
	}
	if !g.inProgress() {
		t.Error("inProgress 应为 true")
	}
	g.release()
	if g.inProgress() {
		t.Error("release 之后 inProgress 应为 false")
	}
	if !g.acquire() {
		t.Error("release 之后应当能再次抢占")
	}
	g.release()
}

// 已有流程在进行时，手动请求应当被明确拒绝，而不是静默地弹第二个框。
func TestUpdatePromptRejectedWhileAnotherInProgress(t *testing.T) {
	s := New(&sessionApps{}, WithUpdate("https://example.invalid/releases.json", "1.3.0"))
	tok := issueAs(t, s, "admin")

	// 自己占住执行权，模拟"启动检查正在弹框"
	if !promptGate.acquire() {
		t.Fatal("包级 promptGate 应处于空闲状态")
	}
	w := postJSON(t, s, "POST", "/api/update/prompt", tok, nil)
	promptGate.release()

	if w.Code != http.StatusConflict {
		t.Errorf("已有更新流程时 = %d，期望 409", w.Code)
	}
	if !strings.Contains(w.Body.String(), "已有一个更新流程在进行中") {
		t.Errorf("响应 %q 里没有说明为什么被拒", w.Body.String())
	}
}
