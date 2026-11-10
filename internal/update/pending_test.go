package update

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 更新确认标记（先决项④）的协议测试。
//
// 这套东西守的是一条很具体的灾难：新版本起不来时，用户没有可用的程序。
// 因此测试的重点不是"能写能读"，而是**在什么情况下绝对不许删 .old**。

// setExeForTest 让 exePath 指向测试造出来的假 exe，测完还原。
func setExeForTest(t *testing.T, exe string) {
	t.Helper()
	prev := exePath
	exePath = func() (string, error) { return exe, nil }
	t.Cleanup(func() { exePath = prev })
}

// withExeDir 让测试在临时目录里扮演"exe 所在目录"。
func withExeDir(t *testing.T) (dir, exe string) {
	t.Helper()
	dir = t.TempDir()
	exe = filepath.Join(dir, "RFERP.exe")
	if err := os.WriteFile(exe, []byte("fake exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	setExeForTest(t, exe)
	return dir, exe
}

// TestPendingRoundTrip 写 → 读 → 清。
func TestPendingRoundTrip(t *testing.T) {
	_, exe := withExeDir(t)

	// 未写之前读不到。
	p, err := ReadPending()
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if p != nil {
		t.Fatalf("还没写标记就读到了: %+v", p)
	}

	if err := writePending(exe); err != nil {
		t.Fatalf("writePending: %v", err)
	}
	p, err = ReadPending()
	if err != nil {
		t.Fatalf("ReadPending: %v", err)
	}
	if p == nil {
		t.Fatal("写完标记却读不到")
	}
	if p.At.IsZero() {
		t.Error("标记里没有时间戳，日志无法说明是哪一次更新")
	}

	ConfirmHealthy()
	p, err = ReadPending()
	if err != nil || p != nil {
		t.Fatalf("确认之后标记应当消失，实际 p=%+v err=%v", p, err)
	}
}

// 核心断言：有标记时 PrepareStart 绝不能删 .old，并且要提出"该问用户了"。
func TestPrepareStartKeepsRollbackAndOffersWhilePending(t *testing.T) {
	_, exe := withExeDir(t)
	oldPath := exe + oldSuffix
	if err := os.WriteFile(oldPath, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePending(exe); err != nil {
		t.Fatal(err)
	}

	d := PrepareStart(false)

	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("有未确认标记时 .old 被删了 —— 新版本若起不来就没有回退副本了")
	}
	if !d.Copy || !d.Unconfirmed {
		t.Errorf("应当报告「有副本、未确认」，实际 %+v", d)
	}
	if !d.OfferRollback {
		t.Error("上一轮始终没走到界面可用，这次启动就该问用户要不要回退")
	}
}

// 正常更新后的第一次启动：磁盘上必然是「.old + 标记」，那是刚换完新版本的
// 样子，不是失败证据。这里如果先弹回退框，用户点默认的「是」就永远升不上去
// （2026-11-10 的现场截图就是这一步）。
func TestPrepareStartDoesNotOfferOnUpdaterFirstStart(t *testing.T) {
	_, exe := withExeDir(t)
	if err := os.WriteFile(exe+oldSuffix, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePending(exe); err != nil {
		t.Fatal(err)
	}

	d := PrepareStart(true)
	if d.OfferRollback {
		t.Fatal("更新后第一次启动就弹回退 —— 用户点默认的「是」会把刚装好的版本退掉")
	}
	if !d.Copy || !d.Unconfirmed {
		t.Errorf("第一次启动仍应保留副本并标记未确认，实际 %+v", d)
	}
	if _, err := os.Stat(exe + oldSuffix); err != nil {
		t.Fatal("更新后第一次启动把 .old 删了")
	}

	// 同一份磁盘状态，下一次普通启动（没有更新器环境变量）才说明上一次
	// 始终没走到界面可用，这时才该问。
	d = PrepareStart(false)
	if !d.OfferRollback {
		t.Error("第二次启动仍不问，回退入口就丢了")
	}
}

// 跨架构升级：1.3.0（Fyne）的 ApplyAndRestart 只做重命名、不写标记。
// 这种「有 .old、无标记」必须被认成待确认并补记，否则最需要回退的那次升级
// 会在第一次启动就把回退副本当成过期文件清掉。
func TestPrepareStartAdoptsMarkerFromLegacyUpgrade(t *testing.T) {
	_, exe := withExeDir(t)
	oldPath := exe + oldSuffix
	if err := os.WriteFile(oldPath, []byte("fyne 1.3.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 刻意不写标记 —— 这正是 1.3.0 升上来时的样子。

	d := PrepareStart(false)

	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("补记之后 .old 必须保留")
	}
	p, err := ReadPending()
	if err != nil || p == nil {
		t.Fatalf("应当补记标记: p=%+v err=%v", p, err)
	}
	if d.OfferRollback {
		t.Error("刚补记标记的这一次启动不该问 —— 还没有任何失败证据，" +
			"否则每次跨架构升级都先吓用户一跳")
	}
}

// 确认健康即删 .old：标记一清，回退副本就再也不会被 offerUpdateRollback 指到，
// 留着只是 18 MB 死文件；删掉它也让「补记」天然只发生一次。
func TestConfirmHealthyRemovesRollbackCopy(t *testing.T) {
	_, exe := withExeDir(t)
	oldPath := exe + oldSuffix
	if err := os.WriteFile(oldPath, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePending(exe); err != nil {
		t.Fatal(err)
	}

	ConfirmHealthy()

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("健康确认之后 %s 应当被清理，实际 err=%v", filepath.Base(oldPath), err)
	}
}

// 确认之后再启动一次：既不该再问，也不该补记（没有 .old 就没什么可补的）。
func TestPrepareStartAfterHealthyConfirmIsQuiet(t *testing.T) {
	_, exe := withExeDir(t)
	if err := os.WriteFile(exe+oldSuffix, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePending(exe); err != nil {
		t.Fatal(err)
	}
	ConfirmHealthy()

	d := PrepareStart(false)

	if d.OfferRollback || d.Unconfirmed || d.Copy {
		t.Errorf("确认健康之后不该有任何残留动作，实际 %+v", d)
	}
	if p, _ := ReadPending(); p != nil {
		t.Error("确认健康之后不该又有标记")
	}
}

// 没有 .old 就什么都不该做：没有副本可指，标记是空壳。
func TestPrepareStartWithoutCopyDoesNothing(t *testing.T) {
	withExeDir(t)

	d := PrepareStart(false)

	if d.Copy || d.Unconfirmed || d.OfferRollback {
		t.Errorf("没有回退副本时不该报告任何待办，实际 %+v", d)
	}
}

// .failed 不归 PrepareStart 管，也不该被健康确认删掉：它是回退弹窗明确承诺
// "保留以便排查"的坏版本。真实演练踩到过——回退后重启的进程一挂载界面就把它清了。
func TestPrepareStartKeepsFailedForDiagnosis(t *testing.T) {
	_, exe := withExeDir(t)
	failedPath := exe + failedSuffix
	if err := os.WriteFile(failedPath, []byte("broken"), 0o644); err != nil {
		t.Fatal(err)
	}

	PrepareStart(false)

	if _, err := os.Stat(failedPath); err != nil {
		t.Fatal("PrepareStart 删掉了 .failed —— 回退之后就没有坏版本可排查了")
	}

	// 健康确认只清 .old；.failed 留着，下一次回退时才被覆盖。
	ConfirmHealthy()
	if _, err := os.Stat(failedPath); err != nil {
		t.Errorf("健康确认删掉了 %s：弹窗承诺的排查材料必须留着，实际 err=%v",
			filepath.Base(failedPath), err)
	}
}

// 标记文件损坏时不能当成"没有标记"：那是删掉唯一回退副本的最好借口。
// 但也不主动问 —— 不知道上一轮到底有没有真的启动过。
func TestPrepareStartKeepsRollbackOnCorruptMarker(t *testing.T) {
	_, exe := withExeDir(t)
	oldPath := exe + oldSuffix
	if err := os.WriteFile(oldPath, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pendingPath(exe), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	p, err := ReadPending()
	if err == nil {
		t.Fatal("损坏的标记应当返回错误，让调用方保守处理")
	}
	if p == nil {
		t.Error("损坏时也应当返回非 nil，表示「有标记但内容不可读」")
	}

	d := PrepareStart(false)

	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("标记损坏时删掉了 .old —— 这正是最需要保守的情况")
	}
	if !d.Unconfirmed {
		t.Error("标记损坏时应当仍按「未确认」处理")
	}
	if d.OfferRollback {
		t.Error("标记损坏时不该主动询问：无法判断上一轮是否真的启动过")
	}
}

// 回退：把 .old 换回来，坏 exe 留作 .failed，标记消失。
func TestRollbackRestoresPreviousAndKeepsFailed(t *testing.T) {
	_, exe := withExeDir(t)
	if err := os.WriteFile(exe+oldSuffix, []byte("previous version"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("broken new version"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePending(exe); err != nil {
		t.Fatal(err)
	}

	restored, err := Rollback()
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if restored != exe {
		t.Errorf("回退到的路径 = %q，期望 %q", restored, exe)
	}

	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "previous version" {
		t.Errorf("回退后 exe 内容 = %q，期望上一版本", body)
	}

	failed, err := os.ReadFile(exe + failedSuffix)
	if err != nil {
		t.Fatalf("坏版本应当保留为 .failed 供排查: %v", err)
	}
	if string(failed) != "broken new version" {
		t.Errorf(".failed 内容 = %q，期望原来的坏版本", failed)
	}

	if p, _ := ReadPending(); p != nil {
		t.Error("回退之后标记还在，下次启动会以为又有一次未确认的更新")
	}
}

// 没有 .old 时回退必须失败，而不是把当前版本弄丢。
func TestRollbackWithoutCopyFails(t *testing.T) {
	_, exe := withExeDir(t)
	if err := writePending(exe); err != nil {
		t.Fatal(err)
	}

	if _, err := Rollback(); err == nil {
		t.Fatal("没有 .old 时回退应当报错")
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		t.Fatalf("回退失败后当前 exe 不见了: %v", err)
	}
	if string(body) != "fake exe" {
		t.Errorf("回退失败后 exe 内容被改了: %q", body)
	}
}

// HasRollbackCopy 只在 .old 真存在时为真。
func TestHasRollbackCopy(t *testing.T) {
	_, exe := withExeDir(t)
	if HasRollbackCopy() {
		t.Fatal("还没有 .old 就报告有回退副本")
	}
	if err := os.WriteFile(exe+oldSuffix, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !HasRollbackCopy() {
		t.Fatal("有 .old 却说没有，会让用户失去回退入口")
	}
}

// 标记的时间戳用 UTC：跨时区排查时不会互相误解。
func TestPendingUsesUTC(t *testing.T) {
	_, exe := withExeDir(t)
	if err := writePending(exe); err != nil {
		t.Fatal(err)
	}
	p, err := ReadPending()
	if err != nil || p == nil {
		t.Fatalf("读标记: p=%+v err=%v", p, err)
	}
	if p.At.Location() != time.UTC {
		t.Errorf("标记时间不是 UTC: %v", p.At.Location())
	}
}

// 失败原因必须在标记里留到下一次启动：第二次弹窗只有固定文案的话，
// 「为什么没起来」就永久丢了（2026-11-10 的备份失败事故）。
func TestRecordFailureSurfacesOnNextStart(t *testing.T) {
	_, exe := withExeDir(t)
	if err := os.WriteFile(exe+oldSuffix, []byte("previous"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writePending(exe); err != nil {
		t.Fatal(err)
	}

	RecordFailure(errors.New("升级前备份失败: 未找到 mysqldump"))

	d := PrepareStart(false)
	if !d.OfferRollback {
		t.Fatal("有标记、有 .old 时应当提供回退")
	}
	if !strings.Contains(d.LastError, "mysqldump") {
		t.Fatalf("LastError = %q，期望包含真实失败原因", d.LastError)
	}
	p, err := ReadPending()
	if err != nil || p == nil || !strings.Contains(p.LastError, "mysqldump") {
		t.Fatalf("标记里没留下失败原因: p=%+v err=%v", p, err)
	}
}

// 没有更新标记时（故障与更新无关）不得凭空创建一个。
func TestRecordFailureWithoutPendingLeavesNoFile(t *testing.T) {
	_, exe := withExeDir(t)

	RecordFailure(errors.New("数据库连不上"))

	if _, err := os.Stat(pendingPath(exe)); !os.IsNotExist(err) {
		t.Fatalf("没有更新标记时不该创建: err=%v", err)
	}
}

// 原因过长时截断，避免把整段 SQL/DSN 写进标记。
func TestRecordFailureTruncatesLongReason(t *testing.T) {
	if got := truncateReason(strings.Repeat("x", 2000)); len([]rune(got)) > 1001 {
		t.Fatalf("截断后仍有 %d 个字符", len([]rune(got)))
	}
}
