package backuplayout

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"app/internal/paths"
)

// 备份布局的关口，重点是**更新兼容性**。
//
// 旧版本把所有备份平铺在 备份\ 根下。用户升级后那些文件必须照样能用。
// 这一组用例守的就是「任何失败模式下备份都不会在界面上消失」：
//
//	迁移失败/被中断/只读  —— List 与 Resolve 仍能看见根目录里的文件
//	迁移只走一半          —— 已移走的与未移走的都要能被列出
//	不认识的文件名        —— 不得被挪走（替用户决定含义）
//	同名冲突              —— 不得覆盖任何一方
//	降级可恢复            —— 每次移动都写日志

func setup(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	paths.SetDataDir(dir)
	t.Cleanup(func() { paths.SetDataDir("") })
	if err := os.MkdirAll(paths.BackupDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func put(t *testing.T, dir, name string, size int) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T) map[string]Entry {
	t.Helper()
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]Entry{}
	for _, e := range list {
		m[e.Name] = e
	}
	return m
}

func TestListFindsLegacyRootFiles(t *testing.T) {
	root := setup(t)
	// 升级前的真实布局：全部平铺在根下。
	put(t, root, filepath.Join("备份", "backup_20260924_170830.sql"), 10)
	put(t, root, filepath.Join("备份", "pre_restore_20260924_172919.sql"), 10)

	got := names(t)
	for _, n := range []string{"backup_20260924_170830.sql", "pre_restore_20260924_172919.sql"} {
		e, ok := got[n]
		if !ok {
			t.Fatalf("升级前的备份 %q 消失了——迁移没跑也不该消失", n)
		}
		if !e.Legacy {
			t.Errorf("%q 应标记为旧布局", n)
		}
		if e.Category != CategoryLegacy {
			t.Errorf("%q 类别 = %q，应为 legacy", n, e.Category)
		}
	}
}

func TestListCoversAllCategories(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "auto", "auto_20261001_120000.sql"), 10)
	put(t, root, filepath.Join("备份", "safety", "pre_clear_20261002_120000.sql"), 10)
	put(t, root, filepath.Join("备份", "manual", "backup_20261003_120000.sql"), 10)

	got := names(t)
	want := map[string]paths.BackupCategory{
		"auto_20261001_120000.sql":      paths.CategoryAuto,
		"pre_clear_20261002_120000.sql": paths.CategorySafety,
		"backup_20261003_120000.sql":    paths.CategoryManual,
	}
	for n, cat := range want {
		e, ok := got[n]
		if !ok {
			t.Fatalf("列出缺少 %q", n)
		}
		if e.Category != cat {
			t.Errorf("%q 类别 = %q，期望 %q", n, e.Category, cat)
		}
		if e.Legacy {
			t.Errorf("%q 已归类，不该标为 legacy", n)
		}
	}
}

func TestListSkipsNonBackupFiles(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "audit_log.csv"), 10)
	put(t, root, filepath.Join("备份", "backup_20261003_120000.sql"), 10)

	got := names(t)
	if _, ok := got["audit_log.csv"]; ok {
		t.Error("csv 不是备份，不该出现在恢复列表里")
	}
	if _, ok := got["backup_20261003_120000.sql"]; !ok {
		t.Error("sql 备份被误删了")
	}
}

// ------------------------------------------------------------------ 迁移

func TestMigrateLegacyMovesByPrefix(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "backup_20260721_154617.sql"), 10)
	put(t, root, filepath.Join("备份", "backup_20260924_170830.final.sql"), 10)
	put(t, root, filepath.Join("备份", "pre_restore_20260924_172919.sql"), 10)
	put(t, root, filepath.Join("备份", "pre_restore_20260924_172919.utf8.sql"), 10)

	rep, err := MigrateLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Moved) != 4 {
		t.Fatalf("应移动 4 个文件，实际 %d（%v）", len(rep.Moved), rep.Moved)
	}

	left, _ := os.ReadDir(paths.BackupDir())
	for _, e := range left {
		if !e.IsDir() && IsBackupExt(e.Name()) {
			t.Errorf("%q 仍在根目录", e.Name())
		}
	}

	// 点分名的 .final 变体必须跟着归到 manual
	if _, err := os.Stat(filepath.Join(paths.ManualBackupDir(), "backup_20260924_170830.final.sql")); err != nil {
		t.Errorf(".final 变体没归到 manual：%v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.SafetyBackupDir(), "pre_restore_20260924_172919.utf8.sql")); err != nil {
		t.Errorf(".utf8 变体没归到 safety：%v", err)
	}
}

func TestMigrateLegacyIsIdempotent(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "backup_20260721_154617.sql"), 10)

	first, err := MigrateLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Moved) != 1 {
		t.Fatalf("第一次应移动 1 个，实际 %d", len(first.Moved))
	}
	second, err := MigrateLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Moved) != 0 {
		t.Errorf("第二次不该再移动，实际 %v", second.Moved)
	}
}

func TestMigrateLegacyLeavesUnknownNamesAlone(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "mybackup.sql"), 10)
	put(t, root, filepath.Join("备份", "backup_20260721_154617.sql"), 10)

	rep, err := MigrateLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(paths.BackupDir(), "mybackup.sql")); err != nil {
		t.Error("无法归类的文件被挪走了——不该替用户决定它的含义")
	}
	if len(rep.Skipped) == 0 {
		t.Error("跳过的文件应记录原因")
	}
	if _, ok := names(t)["mybackup.sql"]; !ok {
		t.Error("无法归类的备份也不该从列表里消失")
	}
}

func TestMigrateLegacyDoesNotOverwrite(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "backup_20260721_154617.sql"), 10)
	put(t, root, filepath.Join("备份", "manual", "backup_20260721_154617.sql"), 99)

	rep, err := MigrateLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Moved) != 0 {
		t.Errorf("不该移动（目标已存在）：%v", rep.Moved)
	}
	st, err := os.Stat(filepath.Join(paths.ManualBackupDir(), "backup_20260721_154617.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 99 {
		t.Errorf("分类目录里的文件被覆盖了：大小 %d，期望 99", st.Size())
	}
	if _, err := os.Stat(filepath.Join(paths.BackupDir(), "backup_20260721_154617.sql")); err != nil {
		t.Error("根目录那份被删了——冲突时不该动任何一方")
	}
}

// 降级可恢复：移动记录写进日志，否则用户降级后备份就在子目录里没人知道。
func TestMigrateLegacyWritesRecoveryLog(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "backup_20260721_154617.sql"), 10)
	put(t, root, filepath.Join("备份", "pre_restore_20260924_172919.sql"), 10)

	if _, err := MigrateLegacy(); err != nil {
		t.Fatal(err)
	}
	lp := filepath.Join(paths.BackupDir(), MigrationLogName)
	b, err := os.ReadFile(lp)
	if err != nil {
		t.Fatalf("没写迁移日志：%v", err)
	}
	s := string(b)
	for _, want := range []string{"backup_20260721_154617.sql", "pre_restore_20260924_172919.sql", "manual", "safety"} {
		if !strings.Contains(s, want) {
			t.Errorf("日志缺少 %q", want)
		}
	}
	if !strings.Contains(s, "降级") {
		t.Error("日志应说明降级后怎么搬回去")
	}
}

// ------------------------------------------------------------------ Resolve

func TestResolveFindsLegacyAndCategorized(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "manual", "backup_20261003_120000.sql"), 10)
	put(t, root, filepath.Join("备份", "backup_20260721_154617.sql"), 10)

	if _, err := Resolve("backup_20261003_120000.sql"); err != nil {
		t.Errorf("分类目录里的文件应能解析：%v", err)
	}
	got, err := Resolve("backup_20260721_154617.sql")
	if err != nil {
		t.Fatalf("旧布局的文件应能解析：%v", err)
	}
	if filepath.Base(filepath.Dir(got)) != "备份" {
		t.Errorf("旧布局文件应解析到根目录，得到 %q", got)
	}
}

// 路径穿越防御不能因为「多目录查找」而松掉。
func TestResolveRejectsTraversal(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "manual", "backup_20261003_120000.sql"), 10)
	outside := filepath.Join(t.TempDir(), "secret.sql")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"",
		"..",
		"..\\..\\secret.sql",
		filepath.Join("..", "secret.sql"),
		outside,
		"C:\\Windows\\System32\\config\\SAM",
		"manual\\backup_20261003_120000.sql",
		"notes.txt",
	} {
		if p, err := Resolve(name); err == nil {
			t.Errorf("文件名 %q 被解析成 %q——存在路径穿越", name, p)
		}
	}
}

func TestResolveMissingFile(t *testing.T) {
	setup(t)
	if _, err := Resolve("backup_20260101_000000.sql"); err == nil {
		t.Error("不存在的文件应报错")
	}
}

func TestResolveRejectsFileSymlinkOutsideBackupRoot(t *testing.T) {
	setup(t)
	outside := t.TempDir()
	const name = "backup_20261003_120000.sql"
	put(t, outside, name, 10)
	if err := os.MkdirAll(paths.ManualBackupDir(), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, name), filepath.Join(paths.ManualBackupDir(), name)); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	if got, err := Resolve(name); err == nil || got != "" {
		t.Fatalf("outside file link accepted: %q, %v", got, err)
	}
}

func TestResolveRejectsCategoryLinkOutsideBackupRoot(t *testing.T) {
	setup(t)
	outside := t.TempDir()
	const name = "backup_20261003_120000.sql"
	put(t, outside, name, 10)
	link := paths.ManualBackupDir()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, outside).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, out)
		}
	} else if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if got, err := Resolve(name); err == nil || got != "" {
		t.Fatalf("outside category link accepted: %q, %v", got, err)
	}
}

// ------------------------------------------------------------------ 写入端

func TestDirForPrefixes(t *testing.T) {
	setup(t)
	cases := map[string]string{
		paths.PrefixManual:     paths.ManualBackupDir(),
		paths.PrefixAuto:       paths.AutoBackupDir(),
		paths.PrefixPreRestore: paths.SafetyBackupDir(),
		paths.PrefixPreClear:   paths.SafetyBackupDir(),
		paths.PrefixPreMigrate: paths.SafetyBackupDir(),
	}
	for prefix, want := range cases {
		got, err := DirFor(prefix)
		if err != nil {
			t.Fatalf("%s: %v", prefix, err)
		}
		if got != want {
			t.Errorf("%s → %q，期望 %q", prefix, got, want)
		}
		if st, err := os.Stat(got); err != nil || !st.IsDir() {
			t.Errorf("%s 的目录没创建：%v", prefix, err)
		}
	}
	if _, err := DirFor("something_else"); err == nil {
		t.Error("未知前缀应报错，而不是猜一个目录")
	}
}

func makeCategoryLink(t *testing.T, link, target string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Fatalf("create junction: %v: %s", err, out)
		}
		return
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestDirForRejectsCategoryLinksOutsideBackupRoot(t *testing.T) {
	setup(t)
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	makeCategoryLink(t, paths.ManualBackupDir(), outside)

	if got, err := DirFor(paths.PrefixManual); err == nil || got != "" {
		t.Fatalf("outside category link accepted: %q, %v", got, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
		t.Fatalf("outside sentinel changed: %q, %v", data, err)
	}
}

func TestDirForRejectsCategoryAliasToAnotherCategory(t *testing.T) {
	setup(t)
	manual := paths.ManualBackupDir()
	if err := os.MkdirAll(manual, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(manual, "backup_20261001_120000.sql")
	if err := os.WriteFile(sentinel, []byte("manual"), 0o600); err != nil {
		t.Fatal(err)
	}
	makeCategoryLink(t, paths.AutoBackupDir(), manual)

	if got, err := DirFor(paths.PrefixAuto); err == nil || got != "" {
		t.Fatalf("auto alias to manual accepted: %q, %v", got, err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "manual" {
		t.Fatalf("manual sentinel changed: %q, %v", data, err)
	}
}

func TestDirForAllowsLinkedBackupRoot(t *testing.T) {
	dataDir := t.TempDir()
	physicalRoot := filepath.Join(t.TempDir(), "backup-root")
	if err := os.MkdirAll(physicalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	paths.SetDataDir(dataDir)
	t.Cleanup(func() { paths.SetDataDir("") })
	makeCategoryLink(t, paths.BackupDir(), physicalRoot)

	got, err := DirFor(paths.PrefixAuto)
	want := filepath.Join(physicalRoot, string(paths.CategoryAuto))
	if err != nil || filepath.Clean(got) != filepath.Clean(want) {
		t.Fatalf("DirFor through linked backup root = %q, %v; want %q", got, err, want)
	}
}

func TestDirForRejectsFileAtCategoryPath(t *testing.T) {
	root := setup(t)
	if err := os.WriteFile(paths.ManualBackupDir(), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := DirFor(paths.PrefixManual); err == nil || got != "" {
		t.Fatalf("file accepted as manual backup directory: %q, %v", got, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "备份", "manual"))
	if err != nil || string(data) != "keep" {
		t.Fatalf("file at category path changed: %q, %v", data, err)
	}
}

func TestMigrateLegacyWorksWithLinkedBackupRoot(t *testing.T) {
	dataDir := t.TempDir()
	physicalRoot := filepath.Join(t.TempDir(), "backup-root")
	if err := os.MkdirAll(physicalRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(physicalRoot, "backup_20261001_120000.sql"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths.SetDataDir(dataDir)
	t.Cleanup(func() { paths.SetDataDir("") })
	makeCategoryLink(t, paths.BackupDir(), physicalRoot)

	rep, err := MigrateLegacy()
	if err != nil || len(rep.Moved) != 1 {
		t.Fatalf("MigrateLegacy through linked backup root = %+v, %v", rep, err)
	}
	if data, err := os.ReadFile(filepath.Join(physicalRoot, "manual", "backup_20261001_120000.sql")); err != nil || string(data) != "legacy" {
		t.Fatalf("migrated file = %q, %v", data, err)
	}
}

func TestMigrateLegacyLeavesFileWhenCategoryLinkEscapes(t *testing.T) {
	root := setup(t)
	const name = "backup_20261001_120000.sql"
	put(t, root, filepath.Join("备份", name), 7)
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	makeCategoryLink(t, paths.ManualBackupDir(), outside)

	rep, err := MigrateLegacy()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Moved) != 0 || len(rep.Skipped) != 1 {
		t.Fatalf("migration report = %+v; expected the legacy file to be skipped", rep)
	}
	if _, err := os.Stat(filepath.Join(paths.BackupDir(), name)); err != nil {
		t.Fatalf("legacy file was not preserved: %v", err)
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
		t.Fatalf("outside sentinel changed: %q, %v", data, err)
	}
}

func TestMigrateLegacyDoesNotWriteThroughMigrationLogLink(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "backup_20261001_120000.sql"), 7)
	outside := filepath.Join(t.TempDir(), MigrationLogName)
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.BackupDir(), MigrationLogName)
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	if _, err := MigrateLegacy(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatalf("outside migration log changed: %q, %v", data, err)
	}
}

func TestMigrateLegacyLeavesFileSymlinkInLegacyRoot(t *testing.T) {
	root := setup(t)
	const name = "backup_20261001_120000.sql"
	outside := filepath.Join(t.TempDir(), "outside.sql")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.BackupDir(), name)
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}

	rep, err := MigrateLegacy()
	if err != nil || len(rep.Moved) != 0 || len(rep.Skipped) != 1 {
		t.Fatalf("MigrateLegacy report = %+v, %v; expected the link to be skipped", rep, err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("legacy source link changed: %v, %v", st, err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(root, "备份", "manual", name)); !os.IsNotExist(err) {
		t.Fatalf("symlink was moved into manual/: %v", err)
	}
}

func TestMigrateLegacyPreservesDanglingDestinationLink(t *testing.T) {
	root := setup(t)
	const name = "backup_20261001_120000.sql"
	put(t, root, filepath.Join("备份", name), 7)
	if err := os.MkdirAll(paths.ManualBackupDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(paths.ManualBackupDir(), name)
	target := filepath.Join(t.TempDir(), "missing.sql")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}

	rep, err := MigrateLegacy()
	if err != nil || len(rep.Moved) != 0 || len(rep.Skipped) != 1 {
		t.Fatalf("MigrateLegacy report = %+v, %v; expected collision to be skipped", rep, err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("dangling destination link changed: %v, %v", st, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("dangling link target was created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(paths.BackupDir(), name)); err != nil {
		t.Fatalf("legacy source was removed: %v", err)
	}
}

func TestCopyFileThenRemovePreservesExistingDestination(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source.sql")
	dst := filepath.Join(root, "manual", "backup_20261001_120000.sql")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFileThenRemove(src, dst); err == nil {
		t.Fatal("copyFileThenRemove overwrote an existing destination")
	}
	for path, want := range map[string]string{src: "source", dst: "existing"} {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
}

// 分类必须与 internal/api 的可恢复命名规则对同一批文件给出一致结论——
// 否则会出现「存进 auto/ 却不被当作可恢复备份」这种错位。
func TestCategoryMatchesRestoreNaming(t *testing.T) {
	real := []string{
		"backup_20260721_154617.sql",
		"backup_20260924_170830.sql",
		"backup_20260924_170830.final.sql",
		"backup_20260924_170830.upgraded.sql",
		"pre_restore_20260924_172919.sql",
		"pre_restore_20260924_172919.utf8.sql",
	}
	for _, n := range real {
		if _, ok := paths.BackupCategoryOf(n); !ok {
			t.Errorf("真实备份 %q 竟然无法归类", n)
		}
	}
	for _, n := range []string{"audit_log.csv", "notes.txt", "mybackup.sql", "backup.sql", "rferp.log"} {
		if _, ok := paths.BackupCategoryOf(n); ok {
			t.Errorf("%q 不该被归类", n)
		}
	}
}

func TestListNewestFirstWithinCategory(t *testing.T) {
	root := setup(t)
	put(t, root, filepath.Join("备份", "manual", "backup_20261001_120000.sql"), 1)
	put(t, root, filepath.Join("备份", "manual", "backup_20261003_120000.sql"), 1)

	old := filepath.Join(paths.ManualBackupDir(), "backup_20261001_120000.sql")
	newer := filepath.Join(paths.ManualBackupDir(), "backup_20261003_120000.sql")
	_ = os.Chtimes(old, time.Now().Add(-72*time.Hour), time.Now().Add(-72*time.Hour))
	_ = os.Chtimes(newer, time.Now(), time.Now())

	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range list {
		if e.Category == paths.CategoryManual {
			got = append(got, e.Name)
		}
	}
	if len(got) != 2 || got[0] != "backup_20261003_120000.sql" {
		t.Errorf("分类内应新→旧，实际 %v", got)
	}
}
