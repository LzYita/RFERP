package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// sqliteQuote 把路径包成 SQL 字符串字面量（单引号转义）。
func sqliteQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// openSQLiteAt 打开（必要时创建）指定路径的 SQLite 库，并返回库与路径。
func openSQLiteAt(t *testing.T, path string) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}
	return db
}

// seedSQLiteV11 造一个"已有业务表、版本停在 v11"的库：基线表 + schema_migrations 1..11，
// 于是 v13 属于 pending 步骤——正是 #28 描述的升级场景。
func seedSQLiteV11(t *testing.T, db *sqlx.DB) {
	t.Helper()
	if err := execAll(db, []string{sqliteSchemaMigrationsDDL}); err != nil {
		t.Fatalf("ensure version table: %v", err)
	}
	if err := applySQLiteBaseline(db); err != nil {
		t.Fatalf("apply baseline: %v", err)
	}
	for _, v := range sqliteBaselineVersions {
		if err := recordVersionSQL(db, v.Version, v.Name); err != nil {
			t.Fatalf("record v%d: %v", v.Version, err)
		}
	}
}

func countSQLiteTable(t *testing.T, db *sqlx.DB, name string) int {
	t.Helper()
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name); err != nil {
		t.Fatalf("count table %s: %v", name, err)
	}
	return n
}

func countRecordedVersion(t *testing.T, db *sqlx.DB, version int) int {
	t.Helper()
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version); err != nil {
		t.Fatalf("count version %d: %v", version, err)
	}
	return n
}

// 有 pending 步骤时，必须先把非空库快照下来，而且快照要发生在步骤之前。
func TestRunSQLiteSnapshotsBeforePendingSteps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.db")
	db := openSQLiteAt(t, path)
	seedSQLiteV11(t, db)

	snapshotDir := filepath.Join(dir, "backups")
	var (
		snapshotCalled    bool
		stepRanAtSnapshot bool
	)
	snapshot := func(dbPath, saveDir string) (string, error) {
		snapshotCalled = true
		probe, err := sqlx.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
		if err != nil {
			return "", err
		}
		defer probe.Close()
		// 快照必须早于步骤：此刻 db_identity 还不该存在。
		var n int
		if err := probe.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='db_identity'`); err != nil {
			return "", err
		}
		stepRanAtSnapshot = n > 0

		if err := os.MkdirAll(saveDir, 0o755); err != nil {
			return "", err
		}
		out := filepath.Join(saveDir, "snapshot.db")
		if _, err := probe.Exec(`VACUUM INTO ` + sqliteQuote(filepath.ToSlash(out))); err != nil {
			return "", err
		}
		return out, nil
	}

	res, err := RunSQLite(db, SQLiteOptions{DBPath: path, SnapshotDir: snapshotDir, Snapshot: snapshot})
	if err != nil {
		t.Fatalf("RunSQLite: %v", err)
	}
	if !snapshotCalled {
		t.Fatal("有 pending 步骤却没有先做升级前快照")
	}
	if stepRanAtSnapshot {
		t.Fatal("快照发生在步骤之后：db_identity 在快照时已存在")
	}
	if !containsVersion(res.Applied, 13) {
		t.Fatalf("applied=%v，期望包含 13", res.Applied)
	}
	if got := countRecordedVersion(t, db, 13); got != 1 {
		t.Fatalf("v13 记录数 = %d，期望 1", got)
	}
	// 快照文件确实落盘且非空
	info, err := os.Stat(filepath.Join(snapshotDir, "snapshot.db"))
	if err != nil {
		t.Fatalf("快照文件不存在: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("快照文件为空")
	}
}

// 快照失败必须中止迁移：步骤不得执行。
func TestRunSQLiteAbortsWhenSnapshotFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.db")
	db := openSQLiteAt(t, path)
	seedSQLiteV11(t, db)

	snapErr := errors.New("snapshot unavailable")
	_, err := RunSQLite(db, SQLiteOptions{
		DBPath:      path,
		SnapshotDir: filepath.Join(dir, "backups"),
		Snapshot:    func(string, string) (string, error) { return "", snapErr },
	})
	if !errors.Is(err, snapErr) {
		t.Fatalf("err = %v，期望包含注入的快照错误", err)
	}
	if got := countRecordedVersion(t, db, 13); got != 0 {
		t.Fatal("快照失败却仍记录了 v13")
	}
	if got := countSQLiteTable(t, db, "db_identity"); got != 0 {
		t.Fatal("快照失败却仍创建了 db_identity")
	}
}

// 非空库 + pending 步骤但未配置快照 → 拒绝升级（fail-closed）。
func TestRunSQLiteRequiresSnapshotConfigOnExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.db")
	db := openSQLiteAt(t, path)
	seedSQLiteV11(t, db)

	if _, err := RunSQLite(db, SQLiteOptions{}); err == nil {
		t.Fatal("未配置快照时不应在非空库上应用步骤")
	}
	if got := countRecordedVersion(t, db, 13); got != 0 {
		t.Fatal("被拒绝的迁移却仍记录了 v13")
	}
}

// 空库初始化不需要快照。
func TestRunSQLiteFreshDatabaseTakesNoSnapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.db")
	db := openSQLiteAt(t, path)

	called := false
	res, err := RunSQLite(db, SQLiteOptions{
		DBPath:      path,
		SnapshotDir: filepath.Join(dir, "backups"),
		Snapshot:    func(string, string) (string, error) { called = true; return "", nil },
	})
	if err != nil {
		t.Fatalf("RunSQLite: %v", err)
	}
	if called {
		t.Fatal("空库初始化不应做快照")
	}
	if res.ToVersion < CurrentSchemaVersion {
		t.Fatalf("ToVersion = %d，期望 >= %d", res.ToVersion, CurrentSchemaVersion)
	}
}

// 没有待执行步骤时既不做快照也不改库。
func TestRunSQLiteNoSnapshotWhenNothingPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "uptodate.db")
	db := openSQLiteAt(t, path)
	if _, err := RunSQLite(db, SQLiteOptions{}); err != nil {
		t.Fatalf("initial RunSQLite: %v", err)
	}

	called := false
	res, err := RunSQLite(db, SQLiteOptions{
		DBPath:      path,
		SnapshotDir: filepath.Join(dir, "backups"),
		Snapshot:    func(string, string) (string, error) { called = true; return "", nil },
	})
	if err != nil {
		t.Fatalf("second RunSQLite: %v", err)
	}
	if called {
		t.Fatal("无待执行步骤不应做快照")
	}
	if len(res.Applied) != 0 {
		t.Fatalf("applied = %v，期望空", res.Applied)
	}
}

func containsVersion(list []int, want int) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
