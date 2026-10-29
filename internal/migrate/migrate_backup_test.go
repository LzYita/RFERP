package migrate

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

// 说明：v12+ 步骤现有四条（v13 db_identity、v14 batch_temp_parts、v15 推导字段、
// v16 part_inbounds、v17 批次剩余），新增步骤时需要在这里补上对应的期望。

// expectMySQLV11WithPendingStep 登记"库停在 v11、v13~v17 待执行、库非空"的完整期望，
// 并把 backupMarker 查询排在步骤之前，用于断言"备份先于步骤"。
func expectMySQLV11WithPendingStep(mock sqlmock.Sqlmock, businessTables int, backupMarker bool) {
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT MAX\\(version\\) FROM schema_migrations").
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(11))
	// pendingStepMigrations：v13~v17 均尚未记录
	for i := 0; i < 6; i++ {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM schema_migrations WHERE version").
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	}
	// databaseEmpty：库非空
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM information_schema.TABLES").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(businessTables))
	if backupMarker {
		// 由注入的备份函数执行：它排在步骤的期望之前，因此一旦步骤先跑，
		// sqlmock 的顺序匹配就会失配并报错。
		mock.ExpectQuery("SELECT 1 AS backup_marker").
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
	}
	// applyStepMigrations 再次查询待执行步骤（v13~v17）
	for i := 0; i < 6; i++ {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM schema_migrations WHERE version").
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	}
	// 步骤本体：建表 + 补一行 + 记录版本
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS db_identity").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM db_identity").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	mock.ExpectExec("INSERT INTO db_identity").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT IGNORE INTO schema_migrations").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// v14 本体：建临时零件表 + 记录版本
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS batch_temp_parts").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT IGNORE INTO schema_migrations").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// v15 本体：逐列检查（都缺）→ 每列一次 ALTER → 记录版本
	for i := 0; i < 3; i++ {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM information_schema.COLUMNS").
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
		mock.ExpectExec("ALTER TABLE batch_temp_parts").
			WillReturnResult(sqlmock.NewResult(0, 0))
	}
	mock.ExpectExec("INSERT IGNORE INTO schema_migrations").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// v16 本体：建入库批次表 + 播种历史库存 + 回填批次号 + 记录版本
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS part_inbounds").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO part_inbounds").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("UPDATE part_inbounds SET batch_no").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT IGNORE INTO schema_migrations").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// v17 本体：补 remaining 列 + 建扣料明细表 + 存量按 FIFO 回填 + 记录版本
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM information_schema.COLUMNS").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	mock.ExpectExec("ALTER TABLE part_inbounds").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS part_inbound_draws").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT id, stock_qty FROM parts").
		WillReturnRows(sqlmock.NewRows([]string{"id", "stock_qty"}))
	mock.ExpectExec("INSERT IGNORE INTO schema_migrations").
		WillReturnResult(sqlmock.NewResult(1, 1))
	// v18 本体：建投料批次分配表 + 记录版本
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS batch_trace_allocs").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT IGNORE INTO schema_migrations").
		WillReturnResult(sqlmock.NewResult(1, 1))
}

// 非空库 + 仅剩 v12+ 步骤时，也必须先做升级前备份，而且备份要早于步骤。
func TestRunBacksUpBeforePendingStepsOnMySQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db := sqlx.NewDb(sqlDB, "mysql")

	expectMySQLV11WithPendingStep(mock, 5, true)

	backupCalled := false
	backup := func(dsn, tool, saveDir string) (string, error) {
		backupCalled = true
		// 标记查询：排在步骤期望之前，用于证明备份确实先执行。
		var marker int
		if err := db.Get(&marker, "SELECT 1 AS backup_marker"); err != nil {
			return "", err
		}
		return filepath.Join(saveDir, "backup.sql"), nil
	}

	res, err := Run(db, Options{DSN: "u:p@tcp(127.0.0.1:3306)/t", BackupDir: t.TempDir(), Backup: backup})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !backupCalled {
		t.Fatal("仅剩 v12+ 步骤时没有做升级前备份")
	}
	if res.BackupPath == "" {
		t.Fatal("Result.BackupPath 为空")
	}
	if !containsVersion(res.Applied, 13) || !containsVersion(res.Applied, 14) ||
		!containsVersion(res.Applied, 15) || !containsVersion(res.Applied, 16) || !containsVersion(res.Applied, 17) {
		t.Fatalf("applied = %v，期望包含 13~17", res.Applied)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("数据库期望未满足: %v", err)
	}
}

// 备份失败必须中止迁移：步骤不得执行。
func TestRunAbortsWhenMySQLBackupFails(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db := sqlx.NewDb(sqlDB, "mysql")

	// 只登记到备份为止：若步骤仍被执行，sqlmock 会报"unexpected call"。
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT MAX\\(version\\) FROM schema_migrations").
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(11))
	for i := 0; i < 6; i++ {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM schema_migrations WHERE version").
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM information_schema.TABLES").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(5))

	backupErr := errors.New("mysqldump unavailable")
	_, err = Run(db, Options{
		DSN:       "u:p@tcp(127.0.0.1:3306)/t",
		BackupDir: t.TempDir(),
		Backup:    func(string, string, string) (string, error) { return "", backupErr },
	})
	if !errors.Is(err, backupErr) {
		t.Fatalf("err = %v，期望包含注入的备份错误", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("数据库期望未满足（可能步骤仍被执行）: %v", err)
	}
}

// 非空库升级但未配置备份目录 → 拒绝（fail-closed）。
func TestRunRefusesNonEmptyUpgradeWithoutBackupDir(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db := sqlx.NewDb(sqlDB, "mysql")

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT MAX\\(version\\) FROM schema_migrations").
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(11))
	for i := 0; i < 6; i++ {
		mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM schema_migrations WHERE version").
			WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM information_schema.TABLES").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(5))

	if _, err := Run(db, Options{DSN: "u:p@tcp(127.0.0.1:3306)/t"}); err == nil {
		t.Fatal("非空库升级未配置备份目录时应中止")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("数据库期望未满足: %v", err)
	}
}

// 没有待执行内容时既不做备份也不改库。
func TestRunNoBackupWhenNothingPendingOnMySQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	db := sqlx.NewDb(sqlDB, "mysql")

	mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT MAX\\(version\\) FROM schema_migrations").
		WillReturnRows(sqlmock.NewRows([]string{"v"}).AddRow(18))
	// cur 已是最新：v13~v18 的版本行都在，pendingStepMigrations 查完即空。

	backupCalled := false
	res, err := Run(db, Options{
		DSN:       "u:p@tcp(127.0.0.1:3306)/t",
		BackupDir: t.TempDir(),
		Backup:    func(string, string, string) (string, error) { backupCalled = true; return "", nil },
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if backupCalled {
		t.Fatal("无待执行内容不应做备份")
	}
	if len(res.Applied) != 0 {
		t.Fatalf("applied = %v，期望空", res.Applied)
	}
	if res.ToVersion != 18 {
		t.Fatalf("ToVersion = %d，期望 18", res.ToVersion)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("数据库期望未满足: %v", err)
	}
}
