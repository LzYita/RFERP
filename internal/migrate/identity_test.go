package migrate

import "testing"

func TestDBIdentityCreatedOnceAndStable(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := RunSQLite(db, SQLiteOptions{}); err != nil {
		t.Fatalf("RunSQLite: %v", err)
	}

	var id string
	if err := db.Get(&id, `SELECT id FROM db_identity`); err != nil {
		t.Fatalf("read identity: %v", err)
	}
	if len(id) != 36 {
		t.Fatalf("identity=%q, want a 36-char UUID", id)
	}

	// 幂等：再跑一次不新增行、不更换身份。
	if _, err := RunSQLite(db, SQLiteOptions{}); err != nil {
		t.Fatalf("second RunSQLite: %v", err)
	}
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM db_identity`); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("db_identity rows=%d, want 1", n)
	}
	var again string
	if err := db.Get(&again, `SELECT id FROM db_identity`); err != nil {
		t.Fatal(err)
	}
	if again != id {
		t.Fatalf("identity changed across runs: %q -> %q", id, again)
	}
}

// 已有库升级时只补行，不换身份；这里模拟"表在但行丢了"的库。
func TestEnsureIdentityRowBackfillsMissingRow(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := RunSQLite(db, SQLiteOptions{}); err != nil {
		t.Fatalf("RunSQLite: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM db_identity`); err != nil {
		t.Fatal(err)
	}
	if err := ensureIdentityRow(db); err != nil {
		t.Fatalf("ensureIdentityRow: %v", err)
	}
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM db_identity`); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("db_identity rows=%d, want 1", n)
	}
}

func TestNewUUIDIsVersion4Shaped(t *testing.T) {
	a, err := newUUID()
	if err != nil {
		t.Fatalf("newUUID: %v", err)
	}
	b, err := newUUID()
	if err != nil {
		t.Fatalf("newUUID: %v", err)
	}
	if a == b {
		t.Fatal("newUUID must not repeat")
	}
	if len(a) != 36 || a[14] != '4' {
		t.Fatalf("uuid=%q, want version 4 shape", a)
	}
}
