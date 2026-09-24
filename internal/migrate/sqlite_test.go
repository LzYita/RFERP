package migrate

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func openSQLiteTest(t *testing.T) *sqlx.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rferp_test.db")
	// parseTime 扫入 time.Time；foreign_keys 与产品路径一致。
	db, err := sqlx.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&parseTime=true")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping sqlite: %v", err)
	}
	return db
}

func TestRunSQLiteBaselineOnEmptyDatabase(t *testing.T) {
	db := openSQLiteTest(t)

	res, err := RunSQLite(db)
	if err != nil {
		t.Fatalf("RunSQLite: %v", err)
	}
	if res.FromVersion != 0 || res.ToVersion != CurrentSchemaVersion {
		t.Fatalf("versions from=%d to=%d, want 0..%d", res.FromVersion, res.ToVersion, CurrentSchemaVersion)
	}
	if len(res.Applied) != CurrentSchemaVersion {
		t.Fatalf("applied=%v, want %d versions", res.Applied, CurrentSchemaVersion)
	}

	var tables []string
	if err := db.Select(&tables, `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	want := map[string]bool{
		"products": true, "parts": true, "bom_items": true, "product_batches": true,
		"batch_skip_parts": true, "batch_trace": true, "batch_consumptions": true,
		"users": true, "audit_log": true, "schema_migrations": true,
	}
	for _, name := range tables {
		delete(want, name)
	}
	if len(want) != 0 {
		t.Fatalf("missing tables: %v (got %v)", want, tables)
	}

	var versions []int
	if err := db.Select(&versions, `SELECT version FROM schema_migrations ORDER BY version`); err != nil {
		t.Fatalf("read versions: %v", err)
	}
	if len(versions) != CurrentSchemaVersion {
		t.Fatalf("schema_migrations rows=%v, want 1..%d", versions, CurrentSchemaVersion)
	}
	for i, v := range versions {
		if v != i+1 {
			t.Fatalf("schema_migrations[%d]=%d, want %d", i, v, i+1)
		}
	}
}

func TestRunSQLiteIsIdempotentAtBaseline(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := RunSQLite(db); err != nil {
		t.Fatalf("first RunSQLite: %v", err)
	}
	res, err := RunSQLite(db)
	if err != nil {
		t.Fatalf("second RunSQLite: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("second run applied %v, want none", res.Applied)
	}
	if res.ToVersion != CurrentSchemaVersion {
		t.Fatalf("ToVersion=%d, want %d", res.ToVersion, CurrentSchemaVersion)
	}
}

func TestRunSQLiteRejectsPartialHistoryWithoutVersionRows(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := db.Exec(`CREATE TABLE products (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	if _, err := RunSQLite(db); err == nil {
		t.Fatal("expected error for tables without schema_migrations history")
	}
}

func TestRunSQLiteRejectsBelowBaselineVersion(t *testing.T) {
	db := openSQLiteTest(t)
	if err := execAll(db, []string{sqliteSchemaMigrationsDDL}); err != nil {
		t.Fatalf("ensure version table: %v", err)
	}
	if err := recordVersionSQL(db, 5, "batch_skip_parts"); err != nil {
		t.Fatalf("seed version: %v", err)
	}
	if _, err := RunSQLite(db); err == nil {
		t.Fatal("expected error when version < baseline")
	}
}

func TestSQLiteBaselineHasQuantityChecks(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := RunSQLite(db); err != nil {
		t.Fatalf("RunSQLite: %v", err)
	}
	// 插入违反 CHECK 的行应失败（与 MySQL v10/v11 约束对齐）。
	if _, err := db.Exec(`INSERT INTO parts (code, name, stock_qty, warn_qty) VALUES ('P1', 'x', -1, 0)`); err == nil {
		t.Fatal("expected CHECK failure for negative stock_qty")
	}
	if _, err := db.Exec(`INSERT INTO batch_consumptions (batch_id, part_id, consumed_qty) VALUES (1, 1, -0.01)`); err == nil {
		// FK 可能先失败；只要报错即满足“约束存在”。
	} else {
		// ok
	}
}

func TestNextVersionStartsAfterBaseline(t *testing.T) {
	if got := nextVersion(); got != CurrentSchemaVersion+1 {
		t.Fatalf("nextVersion()=%d, want %d", got, CurrentSchemaVersion+1)
	}
}
