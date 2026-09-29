package service_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"

	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"
	"app/internal/service"
	"app/internal/usecase"
)

// 整库恢复的端到端验收（#26）。需要设置 RFERP_TEST_DSN 指向隔离测试库，例如：
//
//	rferp_test:@tcp(127.0.0.1:3306)/rferp_restore_test?parseTime=true
//
// 禁止指向业务库：测试会清空目标库中的业务表。
func TestRestoreDatabaseFullRestoreIntegration(t *testing.T) {
	dsn := os.Getenv("RFERP_TEST_DSN")
	if dsn == "" {
		t.Skip("RFERP_TEST_DSN not set; skip restore integration test")
	}
	mysqldump := envOr("RFERP_TEST_MYSQLDUMP", "mysqldump")

	db := connectTestDB(t, dsn)
	resetTestTables(t, db)
	if _, err := migrate.Run(db, migrate.Options{DSN: dsn}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	backupDir := t.TempDir()
	apps := service.New(repository.New(db), dsn, mysqldump, nil)

	// --- 备份时刻的数据 ---
	if _, err := apps.CreateInitialAdmin("restoreadmin", "pass1234", "Restore Admin"); err != nil {
		t.Fatalf("create admin: %v", err)
	}
	keepID := createPart(t, apps, "KEEP-1", "保留零件")
	if _, err := apps.CreatePart(&model.Part{Code: "GONE-1", Name: "将被删除的零件", Unit: "个", Status: 1, Operator: strPtr("tester")}); err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := apps.CreateProduct(&model.Product{Code: "P-KEEP", Name: "保留产品", Unit: "台", Status: 1, Operator: strPtr("tester")}); err != nil {
		t.Fatalf("create product: %v", err)
	}

	backupPath, err := apps.BackupDatabase(backupDir)
	if err != nil {
		t.Skipf("backup tool unavailable (%s): %v", mysqldump, err)
	}
	if _, err := os.Stat(backupPath); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	// --- 备份之后：删除一个零件、入库改数量、再新增一个零件 ---
	if err := apps.DeletePart(partIDByCode(t, db, "GONE-1"), "tester"); err != nil {
		t.Fatalf("delete part: %v", err)
	}
	if err := apps.StockIn(usecase.StockInInput{PartID: keepID, Qty: 999, Operator: "tester"}); err != nil {
		t.Fatalf("stock in: %v", err)
	}
	if _, err := apps.CreatePart(&model.Part{Code: "NEW-1", Name: "备份后新增", Unit: "个", Status: 1, Operator: strPtr("tester")}); err != nil {
		t.Fatalf("create part: %v", err)
	}
	if got := tableCounts(t, db)["parts"]; got != 2 {
		t.Fatalf("setup: parts = %d, want 2", got)
	}

	// --- 执行整库恢复 ---
	res, err := apps.RestoreDatabase(backupPath)
	if err != nil {
		t.Fatalf("RestoreDatabase: %v", err)
	}
	if res.PreRestore == "" {
		t.Fatal("restore did not create a pre-restore copy")
	}
	if _, err := os.Stat(res.PreRestore); err != nil {
		t.Fatalf("pre-restore copy missing: %v", err)
	}
	if !strings.Contains(res.PreRestore, "pre_restore_") {
		t.Errorf("pre-restore copy name = %q, want a pre_restore_ prefix", res.PreRestore)
	}

	after := tableCounts(t, db)

	// 1. 回到备份时刻：备份后新增的零件消失、删除的零件回来、库存回退
	if after["parts"] != 2 {
		t.Errorf("parts = %d, want 2 (back to the backup moment)", after["parts"])
	}
	if got := partStock(t, db, "KEEP-1"); got != 0 {
		t.Errorf("KEEP-1 stock = %v, want 0 (the post-backup stock-in must be rolled back)", got)
	}
	if !partExists(t, db, "GONE-1") {
		t.Error("GONE-1 was deleted after the backup and should be back after a full restore")
	}
	if partExists(t, db, "NEW-1") {
		t.Error("NEW-1 was created after the backup and must be gone")
	}

	// 2. 系统表保留：账号仍在（产品决策：账号不随数据回滚）
	if n := countRows(t, db, "users"); n < 1 {
		t.Errorf("users = %d after restore, want at least the admin account preserved", n)
	}

	// 3. 迁移版本与数据库身份保留
	var migrated int
	if err := db.Get(&migrated, "SELECT COUNT(*) FROM schema_migrations WHERE version >= 13"); err != nil || migrated == 0 {
		t.Errorf("schema_migrations lost the current version (v13 rows=%d, err=%v)", migrated, err)
	}
	var identities int
	if err := db.Get(&identities, "SELECT COUNT(*) FROM db_identity"); err != nil || identities != 1 {
		t.Errorf("db_identity should still hold exactly one row, got %d (err=%v)", identities, err)
	}

	// 4. 恢复动作本身留痕（audit_log 已被备份重建，记录写在提交之后）
	var restores int
	if err := db.Get(&restores, "SELECT COUNT(*) FROM audit_log WHERE action = 'RESTORE'"); err != nil || restores != 1 {
		t.Errorf("expected exactly 1 RESTORE audit row, got %d (err=%v)", restores, err)
	}
}

// TestRestoreDatabaseRollsBackOnBrokenDumpIntegration 损坏的备份必须整笔回滚，
// 且恢复前的数据原封不动。
func TestRestoreDatabaseRollsBackOnBrokenDumpIntegration(t *testing.T) {
	dsn := os.Getenv("RFERP_TEST_DSN")
	if dsn == "" {
		t.Skip("RFERP_TEST_DSN not set; skip restore integration test")
	}
	mysqldump := envOr("RFERP_TEST_MYSQLDUMP", "mysqldump")

	db := connectTestDB(t, dsn)
	resetTestTables(t, db)
	if _, err := migrate.Run(db, migrate.Options{DSN: dsn}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	backupDir := t.TempDir()
	apps := service.New(repository.New(db), dsn, mysqldump, nil)
	if _, err := apps.CreatePart(&model.Part{Code: "ATOMIC-1", Name: "原子性验证", Unit: "个", Status: 1, Operator: strPtr("tester")}); err != nil {
		t.Fatalf("create part: %v", err)
	}
	backupPath, err := apps.BackupDatabase(backupDir)
	if err != nil {
		t.Skipf("backup tool unavailable (%s): %v", mysqldump, err)
	}
	// 构造一个「带完成标记但含非法 INSERT」的备份。
	// 闸门 G1 只看完成标记，真正的防线是事务回滚。
	// 注意不能注入 DDL/会话语句：解析器按设计只执行 INSERT，
	// 其余语句一律忽略（这正是"不改变现有数据库结构"的实现方式）。
	raw, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	marker := "UNLOCK TABLES;"
	if !strings.Contains(string(raw), marker) {
		t.Skip("dump layout changed; cannot inject the invalid statement")
	}
	body := strings.Replace(string(raw), marker,
		"INSERT INTO `parts` (`column_that_does_not_exist`) VALUES (1);\n"+marker, 1)
	broken := filepath.Join(t.TempDir(), "backup_20260101_000000.sql")
	if err := os.WriteFile(broken, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	// 备份之后新增一行，确保「清空后回灌失败」会留下可观测差异
	if _, err := apps.CreatePart(&model.Part{Code: "AFTER-1", Name: "备份后新增", Unit: "个", Status: 1, Operator: strPtr("tester")}); err != nil {
		t.Fatalf("create part: %v", err)
	}
	withExtra := tableCounts(t, db)

	res, err := apps.RestoreDatabase(broken)
	if err == nil {
		t.Fatal("RestoreDatabase accepted a dump containing an invalid statement")
	}
	if res.PreRestore == "" {
		t.Error("a failed restore must still report where the pre-restore copy went")
	}

	after := tableCounts(t, db)
	for table, want := range withExtra {
		if after[table] != want {
			t.Errorf("table %s = %d after a failed restore, want %d (the transaction must roll back)",
				table, after[table], want)
		}
	}
	if !partExists(t, db, "AFTER-1") {
		t.Error("a failed restore must not wipe data added after the backup")
	}
	if !partExists(t, db, "ATOMIC-1") {
		t.Error("a failed restore must not wipe the original data either")
	}
}

// TestRestoreDatabaseRejectsTruncatedDumpIntegration 缺完成标记的文件在碰库之前就被拒。
func TestRestoreDatabaseRejectsTruncatedDumpIntegration(t *testing.T) {
	dsn := os.Getenv("RFERP_TEST_DSN")
	if dsn == "" {
		t.Skip("RFERP_TEST_DSN not set; skip restore integration test")
	}
	mysqldump := envOr("RFERP_TEST_MYSQLDUMP", "mysqldump")

	db := connectTestDB(t, dsn)
	resetTestTables(t, db)
	if _, err := migrate.Run(db, migrate.Options{DSN: dsn}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	apps := service.New(repository.New(db), dsn, mysqldump, nil)
	if _, err := apps.CreatePart(&model.Part{Code: "GATE-1", Name: "闸门验证", Unit: "个", Status: 1, Operator: strPtr("tester")}); err != nil {
		t.Fatalf("create part: %v", err)
	}
	before := tableCounts(t, db)

	truncated := filepath.Join(t.TempDir(), "backup_20260101_000000.sql")
	if err := os.WriteFile(truncated, []byte("INSERT INTO parts (code) VALUES ('X');\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := apps.RestoreDatabase(truncated); err == nil {
		t.Fatal("RestoreDatabase accepted a dump without the completion marker")
	} else if !strings.Contains(err.Error(), "备份文件校验未通过") {
		t.Errorf("RestoreDatabase error = %v, want the verification gate", err)
	}
	if after := tableCounts(t, db); after["parts"] != before["parts"] {
		t.Errorf("parts = %d, want %d: the gate must reject before touching data", after["parts"], before["parts"])
	}
}

// ---- helpers ----

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func connectTestDB(t *testing.T, dsn string) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Connect("mysql", dsn)
	if err != nil {
		t.Fatalf("connect dev db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func resetTestTables(t *testing.T, db *sqlx.DB) {
	t.Helper()
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS batch_consumptions",
		"DROP TABLE IF EXISTS batch_skip_parts",
		"DROP TABLE IF EXISTS batch_trace",
		"DROP TABLE IF EXISTS product_batches",
		"DROP TABLE IF EXISTS bom_items",
		"DROP TABLE IF EXISTS parts",
		"DROP TABLE IF EXISTS products",
		"DROP TABLE IF EXISTS audit_log",
		"DROP TABLE IF EXISTS users",
		"DROP TABLE IF EXISTS schema_migrations",
		"DROP TABLE IF EXISTS db_identity",
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("reset %s: %v", stmt, err)
		}
	}
}

func createPart(t *testing.T, apps usecase.Applications, code, name string) int64 {
	t.Helper()
	p, err := apps.CreatePart(&model.Part{Code: code, Name: name, Unit: "个", Status: 1, Operator: strPtr("tester")})
	if err != nil {
		t.Fatalf("create part %s: %v", code, err)
	}
	return p.ID
}

func partIDByCode(t *testing.T, db *sqlx.DB, code string) int64 {
	t.Helper()
	var id int64
	if err := db.Get(&id, "SELECT id FROM parts WHERE code = ?", code); err != nil {
		t.Fatalf("find part %s: %v", code, err)
	}
	return id
}

func partStock(t *testing.T, db *sqlx.DB, code string) float64 {
	t.Helper()
	var stock float64
	if err := db.Get(&stock, "SELECT stock_qty FROM parts WHERE code = ?", code); err != nil {
		t.Fatalf("read stock of %s: %v", code, err)
	}
	return stock
}

func partExists(t *testing.T, db *sqlx.DB, code string) bool {
	t.Helper()
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM parts WHERE code = ?", code); err != nil {
		t.Fatalf("check part %s: %v", code, err)
	}
	return n > 0
}

func countRows(t *testing.T, db *sqlx.DB, table string) int {
	t.Helper()
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM "+table); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func tableCounts(t *testing.T, db *sqlx.DB) map[string]int {
	t.Helper()
	counts := make(map[string]int)
	for _, table := range []string{
		"parts", "products", "bom_items", "product_batches",
		"batch_consumptions", "batch_skip_parts", "batch_trace", "audit_log",
	} {
		counts[table] = countRows(t, db, table)
	}
	return counts
}
