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

	res, err := RunSQLite(db, SQLiteOptions{})
	if err != nil {
		t.Fatalf("RunSQLite: %v", err)
	}
	if res.FromVersion != 0 || res.ToVersion < CurrentSchemaVersion {
		t.Fatalf("versions from=%d to=%d, want 0..%d", res.FromVersion, res.ToVersion, CurrentSchemaVersion)
	}
	if len(res.Applied) < CurrentSchemaVersion {
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
	if len(versions) < CurrentSchemaVersion {
		t.Fatalf("schema_migrations rows=%v, want at least 1..%d", versions, CurrentSchemaVersion)
	}
	// 版本号必须严格递增且覆盖基线；允许跳号（撤销 v12 后 12 不再存在）。
	seen := map[int]bool{}
	for i, v := range versions {
		if i > 0 && v <= versions[i-1] {
			t.Fatalf("schema_migrations not strictly increasing: %v", versions)
		}
		seen[v] = true
	}
	for v := 1; v <= CurrentSchemaVersion; v++ {
		if !seen[v] {
			t.Fatalf("schema_migrations missing v%d (got %v)", v, versions)
		}
	}
	if last := versions[len(versions)-1]; last < CurrentSchemaVersion {
		t.Fatalf("last version=%d, want >= %d", last, CurrentSchemaVersion)
	}
}

func TestRunSQLiteIsIdempotentAtBaseline(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := RunSQLite(db, SQLiteOptions{}); err != nil {
		t.Fatalf("first RunSQLite: %v", err)
	}
	res, err := RunSQLite(db, SQLiteOptions{})
	if err != nil {
		t.Fatalf("second RunSQLite: %v", err)
	}
	if len(res.Applied) != 0 {
		t.Fatalf("second run applied %v, want none", res.Applied)
	}
	if res.ToVersion < CurrentSchemaVersion {
		t.Fatalf("ToVersion=%d, want %d", res.ToVersion, CurrentSchemaVersion)
	}
}

func TestRunSQLiteRejectsPartialHistoryWithoutVersionRows(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := db.Exec(`CREATE TABLE products (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatalf("seed table: %v", err)
	}
	if _, err := RunSQLite(db, SQLiteOptions{}); err == nil {
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
	if _, err := RunSQLite(db, SQLiteOptions{}); err == nil {
		t.Fatal("expected error when version < baseline")
	}
}

func TestSQLiteBaselineHasQuantityChecks(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := RunSQLite(db, SQLiteOptions{}); err != nil {
		t.Fatalf("RunSQLite: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO parts (code, name, stock_qty, warn_qty) VALUES ('P1', 'x', -1, 0)`); err == nil {
		t.Fatal("expected CHECK failure for negative stock_qty")
	}
	// Seed parents then violate batch_consumptions CHECK.
	if _, err := db.Exec(`INSERT INTO products (code,name,unit) VALUES ('G1','g','个')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO parts (code,name,unit,stock_qty,warn_qty) VALUES ('G1-C','c','个',0,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO product_batches (batch_no,product_id,plan_qty) SELECT 'G1-B', id, 1 FROM products WHERE code='G1'`); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec(`INSERT INTO batch_consumptions (batch_id, part_id, consumed_qty)
		SELECT b.id, p.id, -0.01 FROM product_batches b, parts p WHERE b.batch_no='G1-B' AND p.code='G1-C'`)
	if err == nil {
		t.Fatal("expected CHECK failure for negative consumed_qty")
	}
}

func TestNextVersionStartsAfterBaseline(t *testing.T) {
	if got := nextVersion(); got < CurrentSchemaVersion+1 {
		t.Fatalf("nextVersion()=%d, want %d", got, CurrentSchemaVersion+1)
	}
}
