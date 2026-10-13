package service

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"app/internal/config"
	"app/internal/paths"
)

// 自动定期备份的关口。
//
// 三类断言：
//
//	间隔语义 —— 依据「最新一份自动备份的时间」而不是「上次检查时间」，
//	             否则软件关三天再打开会把间隔越攒越长
//	首次立刻 —— 开启后一份都没有时马上补一份，用户才能确认它在工作
//	滚动保留 —— 只删 auto/ 里的旧文件，manual/ 与 safety/ 一个都不许动

// writingSnapshotPort 会**真的写出文件**的快照桩。
//
// 不能复用同包那个 fakeSnapshotPort：它只记录前缀并返回一个路径，不落盘。
// 而自动备份的判断依据是「auto/ 目录里最新一份的时间」——文件不存在，
// 每次都判定成"一份都没有"，于是"没到期不该重复备份"这类断言根本测不到。
type writingSnapshotPort struct {
	fakeSnapshotPort
	n int
}

func (p *writingSnapshotPort) PrefixSnapshot(saveDir, prefix string) (string, error) {
	if err := os.MkdirAll(saveDir, 0o755); err != nil {
		return "", err
	}
	p.n++
	// 时间段用递增计数而不是真时间：PruneSnapshots 按文件名字典序判新旧，
	// 补零的计数天然满足"字典序即时间序"。
	name := fmt.Sprintf("%s_20260101_%06d.sql", prefix, p.n)
	full := filepath.Join(saveDir, name)
	if err := os.WriteFile(full, []byte("-- Dump completed\n"), 0o600); err != nil {
		return "", err
	}
	p.prefixes = append(p.prefixes, prefix)
	return full, nil
}

func newAutoBackupService(t *testing.T, days int) *Service {
	t.Helper()
	dir := t.TempDir()
	paths.SetDataDir(dir)
	t.Cleanup(func() { paths.SetDataDir("") })
	svc, _ := newRestoreMockService(t, &writingSnapshotPort{})
	svc.cfg = &config.Config{AutoBackupDays: days}
	return svc
}

// autoDir 列出 auto/ 目录里的文件名。
func autoFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(paths.AutoBackupDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestMaybeAutoBackupDisabledDoesNothing(t *testing.T) {
	svc := newAutoBackupService(t, 0)
	ran, err := svc.MaybeAutoBackup(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("间隔为 0（关闭）时不该备份")
	}
	if len(autoFiles(t)) != 0 {
		t.Error("关闭时不该产生任何文件")
	}
}

// 开启后一份都没有 → 立刻跑一次。用户刚开启就该看到结果，
// 否则他无法判断这套东西到底工作不工作。
func TestMaybeAutoBackupRunsImmediatelyWhenNoneExists(t *testing.T) {
	svc := newAutoBackupService(t, 7)
	ran, err := svc.MaybeAutoBackup(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("开启后第一次必须立刻备份")
	}
	files := autoFiles(t)
	if len(files) != 1 {
		t.Fatalf("应产生 1 份自动备份，实际 %v", files)
	}
	if got := files[0]; len(got) < len("auto_") || got[:len("auto_")] != "auto_" {
		t.Errorf("文件名应以 auto_ 开头，实际 %q", got)
	}
}

// 没到期就不重复备份。
func TestMaybeAutoBackupSkipsWhenNotDue(t *testing.T) {
	svc := newAutoBackupService(t, 7)
	base := time.Now()
	if _, err := svc.MaybeAutoBackup(base); err != nil {
		t.Fatal(err)
	}
	ran, err := svc.MaybeAutoBackup(base.Add(72 * time.Hour)) // 3 天后
	if err != nil {
		t.Fatal(err)
	}
	if ran {
		t.Error("距上一份 3 天、间隔 7 天时不该再备份")
	}
	if len(autoFiles(t)) != 1 {
		t.Errorf("应仍是 1 份，实际 %v", autoFiles(t))
	}
}

// 到期就补一份。判断依据是「距最新一份多久」，所以软件关了很久
// 再打开时会立刻补上，而不是把间隔一直顺延。
func TestMaybeAutoBackupRunsWhenDue(t *testing.T) {
	svc := newAutoBackupService(t, 3)
	base := time.Now()
	if _, err := svc.MaybeAutoBackup(base); err != nil {
		t.Fatal(err)
	}
	// 把 auto/ 里那一份的时间改老，模拟"放了 4 天"
	files := autoFiles(t)
	old := filepath.Join(paths.AutoBackupDir(), files[0])
	past := base.Add(-4 * 24 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	ran, err := svc.MaybeAutoBackup(base)
	if err != nil {
		t.Fatal(err)
	}
	if !ran {
		t.Error("距上一份 4 天、间隔 3 天时应该补一份")
	}
	if len(autoFiles(t)) != 2 {
		t.Errorf("应有 2 份，实际 %v", autoFiles(t))
	}
}

// 滚动保留 20，且**只删 auto/ 里的**：手动备份永不自动删除是明确的产品要求。
func TestMaybeAutoBackupPrunesOnlyAutoDir(t *testing.T) {
	svc := newAutoBackupService(t, 1)

	// 手动备份两份
	manualDir := paths.ManualBackupDir()
	if err := os.MkdirAll(manualDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"backup_20260101_000000.sql", "backup_20260102_000000.sql"} {
		if err := os.WriteFile(filepath.Join(manualDir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// 操作前副本一份
	safetyDir := paths.SafetyBackupDir()
	if err := os.MkdirAll(safetyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(safetyDir, "pre_clear_20260101_000000.sql"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 造 25 份自动备份，时间各不相同
	autoDir := paths.AutoBackupDir()
	if err := os.MkdirAll(autoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := time.Now()
	for i := 0; i < 25; i++ {
		n := fmt.Sprintf("auto_20260101_%06d.sql", i)
		p := filepath.Join(autoDir, n)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		// 最新的一份也放到 40 天前：间隔是 1 天，所以这次调用**确实到期**。
		// 若最新那份是"现在"，判定为未到期就不会跑，也就不会清理——
		// 那是正确行为（没产生新备份就不需要腾位置），但测不到滚动清理。
		ts := base.Add(-time.Duration(40+i) * time.Hour)
		if err := os.Chtimes(p, ts, ts); err != nil {
			t.Fatal(err)
		}
	}
	before := len(autoFiles(t))

	if _, err := svc.MaybeAutoBackup(base); err != nil {
		t.Fatal(err)
	}

	after := len(autoFiles(t))
	if after > paths.AutoBackupKeep {
		t.Errorf("自动备份 %d 份，超过保留上限 %d", after, paths.AutoBackupKeep)
	}
	if before <= paths.AutoBackupKeep {
		t.Skipf("前置条件不成立：只造了 %d 份", before)
	}
	if after >= before {
		t.Errorf("应清掉旧的：%d -> %d", before, after)
	}

	// 手动与操作前副本必须一个不少
	for _, n := range []string{"backup_20260101_000000.sql", "backup_20260102_000000.sql"} {
		if _, err := os.Stat(filepath.Join(manualDir, n)); err != nil {
			t.Errorf("手动备份 %q 被误删——手动备份永不自动删除", n)
		}
	}
	if _, err := os.Stat(filepath.Join(safetyDir, "pre_clear_20260101_000000.sql")); err != nil {
		t.Error("操作前副本被误删——清理必须只发生在 auto/ 里")
	}
}

func TestSetAutoBackupDaysRejectsInvalid(t *testing.T) {
	svc := newAutoBackupService(t, 0)
	for _, bad := range []int{-1, 4, 6, 14, 90} {
		if err := svc.SetAutoBackupDays(bad); err == nil {
			t.Errorf("间隔 %d 天应被拒绝", bad)
		}
	}
	for _, ok := range []int{0, 1, 2, 3, 5, 7, 30} {
		if err := svc.SetAutoBackupDays(ok); err != nil {
			t.Errorf("间隔 %d 天应被接受：%v", ok, err)
		}
	}
	// 被拒的值不得悄悄改成别的——用户以为每 4 天一份、实际每 5 天，
	// 真正需要备份的那一刻正好落在空档里。
	if err := svc.SetAutoBackupDays(4); err == nil {
		if svc.AutoBackupDays() == 5 || svc.AutoBackupDays() == 3 {
			t.Errorf("被拒的值被悄悄改成了 %d", svc.AutoBackupDays())
		}
	}
}

func TestAutoBackupStatusReportsNextDue(t *testing.T) {
	svc := newAutoBackupService(t, 7)
	st := svc.AutoBackupStatus()
	if !st.Enabled || st.Days != 7 {
		t.Errorf("状态未反映设置：%+v", st)
	}
	if st.Keep != paths.AutoBackupKeep {
		t.Errorf("Keep = %d，期望 %d", st.Keep, paths.AutoBackupKeep)
	}
	if _, err := svc.MaybeAutoBackup(time.Now()); err != nil {
		t.Fatal(err)
	}
	st = svc.AutoBackupStatus()
	if st.Count != 1 {
		t.Errorf("Count = %d，期望 1", st.Count)
	}
	if st.Last == "" || st.NextDue == "" {
		t.Errorf("跑过一次之后应有 last 与 nextDue：%+v", st)
	}
}
