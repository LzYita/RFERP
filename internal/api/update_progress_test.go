package api

import (
	"net/http"
	"testing"
	"time"
)

// 异步下载（?async=1）+ 进度查询：给界面一条能看到"正在下载 x%"的路。
//
// 为什么要有：同步模式下整个下载期间 HTTP 连接被占住，前端只能盯着转圈图标，
// 而一个几十 MB 的包在慢网络上要好几分钟——用户没有依据判断该不该继续等。
//
// 这个文件守四件事：
//  1. 异步模式立刻回 202 + job id，不等下载完；
//  2. 进度端点如实反映字节数与三种终态（running / done / error）；
//  3. **下载失败绝不留下"可 apply"的槽位**——否则界面上点重试会 apply 一个下了一半的包；
//  4. 同步模式（不带 async）行为不变——脚本与既有测试都靠它。

// 1. 异步模式：立刻 202 + job id，此时下载还在后台跑。
func TestAsyncDownloadReturnsJobImmediately(t *testing.T) {
	payload := fakePackage()
	manifestURL, stop := startSignedManifestServer(t, "2.0.1", payload)
	defer stop()

	s := New(&sessionApps{}, WithUpdate(manifestURL, "1.3.0"),
		WithUpdateApplier(func(string) error { return nil }),
		WithProcessExit(func() {}))
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/update/download?async=1", tok, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("异步下载 = %d，期望 202（必须立刻返回，不能等下载完）", w.Code)
	}
	out := decodeBody(t, w)
	jobID, _ := out["jobId"].(string)
	if jobID == "" {
		t.Fatal("响应里没有 jobId：前端没法轮询进度")
	}
	if out["version"] != "2.0.1" {
		t.Errorf("version = %v，期望 2.0.1", out["version"])
	}

	// 让它跑完，确认这条链真的能走通（否则 202 可能只是"接受了"而背后全错）
	done := waitProgress(t, s, tok, jobID, 5*time.Second)
	if done["state"] != "done" {
		t.Fatalf("异步下载终态 = %v（error: %v），期望 done", done["state"], done["error"])
	}
}

// 2. 进度端点：完成时 done == total（进度条要能走到 100%），且带 path。
func TestProgressReportsDoneWithFullBytes(t *testing.T) {
	payload := fakePackage()
	manifestURL, stop := startSignedManifestServer(t, "2.0.1", payload)
	defer stop()

	s := New(&sessionApps{}, WithUpdate(manifestURL, "1.3.0"),
		WithUpdateApplier(func(string) error { return nil }),
		WithProcessExit(func() {}))
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/update/download?async=1", tok, nil)
	jobID := decodeBody(t, w)["jobId"].(string)

	last := waitProgress(t, s, tok, jobID, 5*time.Second)
	if last["state"] != "done" {
		t.Fatalf("终态 = %v（error: %v），期望 done", last["state"], last["error"])
	}
	if p, _ := last["path"].(string); p == "" {
		t.Error("done 状态里没有 path：apply 需要它")
	}
	if got, total := last["done"], last["total"]; got != total {
		t.Errorf("完成时 done=%v total=%v，应相等（进度条要能走到 100%%）", got, total)
	}
	if got, want := last["version"], "2.0.1"; got != want {
		t.Errorf("version = %v，期望 %v", got, want)
	}
}

// 3. 最要紧的一条：下载失败不许留下可 apply 的槽位。
//
// apply 只认"刚下载并校验过"的那个文件。如果失败也登记，界面上点重试就可能
// apply 一个下了一半的包——而 apply 前的哈希核对能挡住它，但那已经是最后一道防线，
// 不该指望它兜住上游的错误。
func TestFailedAsyncDownloadLeavesNoPendingUpdate(t *testing.T) {
	payload := fakePackage()
	manifestURL, stop := startSignedManifestServer(t, "2.0.1", payload)
	defer stop()

	s := New(&sessionApps{}, WithUpdate(manifestURL, "1.3.0"),
		WithUpdateApplier(func(string) error { return nil }),
		WithProcessExit(func() {}))
	tok := issueAdmin(t, s)

	// 先占一个槽位，确保断言测的是"失败没有覆盖/留下它"，而不是"槽位本来就是空的"
	w := doJSON(t, s, "POST", "/api/update/download?async=1", tok, nil)
	jobID := decodeBody(t, w)["jobId"].(string)
	waitProgress(t, s, tok, jobID, 5*time.Second)

	if _, _, none := s.takePendingUpdate(); none {
		t.Fatal("前置条件不成立：成功的异步下载本该登记待应用文件")
	}
	s.clearPendingUpdate()

	// 让这次下载失败：把清单地址换成一个必然失败的源
	// （用同一个 job 通路制造失败，最直接的办法是关掉服务器再请求）
	stop()

	w2 := doJSON(t, s, "POST", "/api/update/download?async=1", tok, nil)
	if w2.Code == http.StatusAccepted {
		job2 := decodeBody(t, w2)["jobId"].(string)
		last := waitProgress(t, s, tok, job2, 30*time.Second)
		if last["state"] != "error" {
			t.Fatalf("源已关闭却报 state=%v，期望 error", last["state"])
		}
		if msg, _ := last["error"].(string); msg == "" {
			t.Error("error 状态里没有原因：界面没法告诉用户发生了什么")
		}
	}

	// 关键：apply 仍然必须拒（没有待应用文件）
	aw := doJSON(t, s, "POST", "/api/update/apply", tok, map[string]any{"confirm": true})
	if aw.Code == http.StatusOK {
		t.Fatal("下载失败却能 apply：留下了一个未校验完的待应用文件，这是危险的")
	}
	if _, _, none := s.takePendingUpdate(); !none {
		t.Error("下载失败后仍留有可 apply 的槽位")
	}
}

// 4. 同步模式（不带 async）行为不变：等下载完才响应，且直接返回 path。
func TestSyncDownloadUnchanged(t *testing.T) {
	payload := fakePackage()
	manifestURL, stop := startSignedManifestServer(t, "2.0.1", payload)
	defer stop()

	s := New(&sessionApps{}, WithUpdate(manifestURL, "1.3.0"),
		WithUpdateApplier(func(string) error { return nil }),
		WithProcessExit(func() {}))
	tok := issueAdmin(t, s)

	w := doJSON(t, s, "POST", "/api/update/download", tok, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("同步下载 = %d，期望 200（%s）", w.Code, w.Body.String())
	}
	out := decodeBody(t, w)
	if p, _ := out["path"].(string); p == "" {
		t.Error("同步模式没有返回 path")
	}
	if _, _, none := s.takePendingUpdate(); none {
		t.Error("同步下载成功后没有登记待应用文件")
	}
}

// 进度端点自身的边界。
func TestProgressEndpointEdges(t *testing.T) {
	s := New(&sessionApps{}, WithUpdate("https://example.invalid/releases.json", "1.3.0"),
		WithUpdateApplier(func(string) error { return nil }),
		WithProcessExit(func() {}))
	tok := issueAdmin(t, s)

	if w := doJSON(t, s, "GET", "/api/update/progress", tok, nil); w.Code != http.StatusBadRequest {
		t.Errorf("缺 job 参数 = %d，期望 400", w.Code)
	}
	if w := doJSON(t, s, "GET", "/api/update/progress?job=nope", tok, nil); w.Code != http.StatusNotFound {
		t.Errorf("不存在的 job = %d，期望 404", w.Code)
	}
	// 未登录不得查进度：它会泄露正在下载的版本与字节数
	if w := doJSON(t, s, "GET", "/api/update/progress?job=x", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("匿名查进度 = %d，期望 401", w.Code)
	}
}

// job 表不该无限增长。
func TestJobTableIsBounded(t *testing.T) {
	s := New(&sessionApps{})
	for i := 0; i < maxJobs*3; i++ {
		j := newDownloadJob(s.newDownloadJobID(), "2.0.1", 100)
		s.setJob(j)
		j.finish(downloadResult{path: "x"})
	}
	s.jobsMu.Lock()
	n := len(s.jobs)
	s.jobsMu.Unlock()
	if n > maxJobs {
		t.Errorf("任务表里有 %d 条，超过上限 %d：会随时间无限增长", n, maxJobs)
	}

	// 完成很久之后的记录应当被顺手清掉
	old := newDownloadJob("ancient", "2.0.1", 100)
	old.finish(downloadResult{path: "x"})
	old.at = time.Now().Add(-jobKeepDuration - time.Minute)
	s.setJob(old)
	if s.job("ancient") != nil {
		t.Error("过期的任务记录没有被清理")
	}
}
