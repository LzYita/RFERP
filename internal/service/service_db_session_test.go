package service

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"app/internal/repository"
)

// fakeSnapshotPort 是不碰真实 mysqldump 的快照端口。
// destructive 会让"操作前副本"失败，用来验证 fail-closed 行为。
type fakeSnapshotPort struct {
	kind        string
	destructive bool
	prefixes    []string
	restored    string
	restoreErr  error
}

func (p *fakeSnapshotPort) Snapshot(saveDir string) (string, error) {
	return p.PrefixSnapshot(saveDir, "backup")
}

func (p *fakeSnapshotPort) PrefixSnapshot(saveDir, prefix string) (string, error) {
	if p.destructive {
		return "", errors.New("snapshot tool unavailable")
	}
	p.prefixes = append(p.prefixes, prefix)
	return filepath.Join(saveDir, prefix+"_20260101_000000.sql"), nil
}

func (p *fakeSnapshotPort) Kind() string { return p.kind }

func (p *fakeSnapshotPort) Restore(snapshotPath string) (string, error) {
	p.restored = snapshotPath
	if p.restoreErr != nil {
		return "", p.restoreErr
	}
	return "before-restore-copy", nil
}

// newRestoreMockService 返回带假快照端口的 Service。
func newRestoreMockService(t *testing.T, port *fakeSnapshotPort) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	t.Setenv("MES_DATA_DIR", t.TempDir())
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewWithSnapshot(repository.New(sqlx.NewDb(db, "sqlmock")), port, nil)
	return svc, mock
}

// writeDump 写一个带 mysqldump 完成标记的备份文件。
func writeDump(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "backup_20260101_000000.sql")
	content := body + "\n-- Dump completed on 2026-01-01 00:00:00\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write dump fixture: %v", err)
	}
	return path
}

func expectSessionChecks(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT @@FOREIGN_KEY_CHECKS AS foreign_key_checks, @@UNIQUE_CHECKS AS unique_checks")).
		WillReturnRows(sqlmock.NewRows([]string{"foreign_key_checks", "unique_checks"}).AddRow(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 0")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("SET UNIQUE_CHECKS = 0")).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func expectSessionChecksRestored(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 1")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("SET UNIQUE_CHECKS = 1")).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func expectBusinessDeletes(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	for _, table := range businessTables {
		mock.ExpectExec(regexp.QuoteMeta("DELETE FROM " + table)).
			WillReturnResult(sqlmock.NewResult(0, 1))
	}
}

// TestRestoreDatabasePropagatesSessionSetupErrorAndRestoresChecks 会话设置失败必须上报。
func TestRestoreDatabasePropagatesSessionSetupErrorAndRestoresChecks(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, mock := newRestoreMockService(t, port)
	path := writeDump(t, "INSERT INTO products (code) VALUES ('P-1');")

	setupErr := errors.New("cannot disable foreign key checks")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT @@FOREIGN_KEY_CHECKS AS foreign_key_checks, @@UNIQUE_CHECKS AS unique_checks")).
		WillReturnRows(sqlmock.NewRows([]string{"foreign_key_checks", "unique_checks"}).AddRow(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 0")).
		WillReturnError(setupErr)
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 1")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("SET UNIQUE_CHECKS = 1")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if _, err := svc.RestoreDatabase(path); err == nil {
		t.Fatal("RestoreDatabase swallowed the session setup error")
	} else if !errors.Is(err, setupErr) {
		t.Fatalf("RestoreDatabase returned the wrong error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

// TestRestoreDatabaseRejectsDumpWithoutCompletionMarker 闸门 G1：
// 缺完成标记的截断文件不得触碰数据库。
func TestRestoreDatabaseRejectsDumpWithoutCompletionMarker(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, mock := newRestoreMockService(t, port)
	path := filepath.Join(t.TempDir(), "truncated.sql")
	if err := os.WriteFile(path, []byte("INSERT INTO products (code) VALUES ('P-1');\n"), 0o600); err != nil {
		t.Fatalf("write truncated fixture: %v", err)
	}

	if _, err := svc.RestoreDatabase(path); err == nil {
		t.Fatal("RestoreDatabase accepted a dump without the completion marker")
	} else if !strings.Contains(err.Error(), "备份文件校验未通过") {
		t.Fatalf("RestoreDatabase error = %v, want verification failure", err)
	}
	if len(port.prefixes) != 0 {
		t.Fatalf("restore created a safety copy before validating the file: %v", port.prefixes)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

// TestRestoreDatabaseAbortsWhenSafetyCopyFails 副本失败必须中止，且不动数据库。
func TestRestoreDatabaseAbortsWhenSafetyCopyFails(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql", destructive: true}
	svc, mock := newRestoreMockService(t, port)
	path := writeDump(t, "INSERT INTO products (code) VALUES ('P-1');")

	_, err := svc.RestoreDatabase(path)
	if err == nil {
		t.Fatal("RestoreDatabase proceeded without a safety copy")
	}
	if !strings.Contains(err.Error(), "已中止恢复") {
		t.Fatalf("RestoreDatabase error = %v, want abort context", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("restore touched the database: %v", err)
	}
}

func TestRestoreSQLiteCreatesSelectableSafetyCopyBeforeReplacingFile(t *testing.T) {
	t.Setenv("MES_DATA_DIR", t.TempDir())
	port := &fakeSnapshotPort{kind: "sqlite"}
	svc := NewWithSnapshot(nil, port, nil)
	path := filepath.Join(t.TempDir(), "backup_20260101_000000.db")
	if err := os.WriteFile(path, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := svc.RestoreDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(port.prefixes) != 1 || port.prefixes[0] != prefixPreRestore {
		t.Fatalf("safety copy prefixes = %v, want pre_restore", port.prefixes)
	}
	if !strings.Contains(res.PreRestore, "pre_restore_") {
		t.Fatalf("reported copy %q is not selectable by the backup UI", res.PreRestore)
	}
	if port.restored != path {
		t.Fatalf("restored %q, want %q", port.restored, path)
	}
}

func TestRestoreSQLiteStopsBeforeReplacingFileWhenSafetyCopyFails(t *testing.T) {
	t.Setenv("MES_DATA_DIR", t.TempDir())
	port := &fakeSnapshotPort{kind: "sqlite", destructive: true}
	svc := NewWithSnapshot(nil, port, nil)
	path := filepath.Join(t.TempDir(), "backup_20260101_000000.db")
	if err := os.WriteFile(path, []byte("SQLite format 3\x00"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.RestoreDatabase(path); err == nil {
		t.Fatal("SQLite restore proceeded without a safety copy")
	}
	if port.restored != "" {
		t.Fatalf("restore ran despite safety-copy failure: %q", port.restored)
	}
}

// TestRestoreDatabaseClearsBusinessTablesBeforeReplayingDump 整库恢复 = 先清空再回灌，
// 且只清业务表。
func TestRestoreDatabaseClearsBusinessTablesBeforeReplayingDump(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, mock := newRestoreMockService(t, port)
	path := writeDump(t,
		"INSERT INTO `products` (`code`) VALUES ('P-1');\n"+
			"INSERT INTO `schema_migrations` (`version`) VALUES (1);\n"+
			"INSERT INTO `users` (`username`) VALUES ('old');\n"+
			"INSERT INTO `db_identity` (`id`) VALUES ('old-id');")

	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	// 只执行 products 的 INSERT；系统表的三条必须被跳过。
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO `products` (`code`) VALUES ('P-1');")).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	expectSessionChecksRestored(t, mock)
	// 恢复后统计行数
	mock.ExpectBegin()
	for _, table := range businessTables {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM " + table)).
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	}
	mock.ExpectCommit()
	// 恢复后的审计留痕
	mock.ExpectExec("INSERT INTO audit_log").
		WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := svc.RestoreDatabase(path)
	if err != nil {
		t.Fatalf("RestoreDatabase: %v", err)
	}
	if res.Statements != 1 {
		t.Fatalf("RestoreDatabase statements = %d, want 1 (system tables skipped)", res.Statements)
	}
	if len(res.TableCounts) != len(businessTables) {
		t.Fatalf("RestoreDatabase counts = %v, want %d entries", res.TableCounts, len(businessTables))
	}
	if len(port.prefixes) != 1 || port.prefixes[0] != prefixPreRestore {
		t.Fatalf("safety copy prefixes = %v, want [%s]", port.prefixes, prefixPreRestore)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

// TestRestoreDatabaseRollsBackAndKeepsSafetyCopy 回灌失败必须整体回滚，
// 但恢复前副本仍要保留，供用户回退。
func TestRestoreDatabaseRollsBackAndKeepsSafetyCopy(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, mock := newRestoreMockService(t, port)
	path := writeDump(t, "INSERT INTO products (code) VALUES ('P-1');")

	insertErr := errors.New("duplicate product")
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO products (code) VALUES ('P-1');")).
		WillReturnError(insertErr)
	mock.ExpectRollback()
	expectSessionChecksRestored(t, mock)

	res, err := svc.RestoreDatabase(path)
	if !errors.Is(err, insertErr) {
		t.Fatalf("RestoreDatabase returned the wrong error: %v", err)
	}
	if res.PreRestore == "" {
		t.Fatal("RestoreDatabase dropped the safety copy path on failure")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

// TestRestoreDatabaseRejectsUnterminatedInsertAndRollsBack 截断语句必须回滚。
func TestRestoreDatabaseRejectsUnterminatedInsertAndRollsBack(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, mock := newRestoreMockService(t, port)
	path := writeDump(t, "INSERT INTO products (code) VALUES ('P-1')")

	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectRollback()
	expectSessionChecksRestored(t, mock)

	_, err := svc.RestoreDatabase(path)
	if err == nil {
		t.Fatal("RestoreDatabase accepted an unterminated INSERT")
	}
	if !strings.Contains(err.Error(), "unterminated INSERT") {
		t.Fatalf("RestoreDatabase error = %v, want unterminated INSERT context", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

// TestRestoreDatabaseSkipsSQLiteSnapshotOnMySQL 跨后端仍必须拒绝。
func TestRestoreDatabaseSkipsSQLiteSnapshotOnMySQL(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, _ := newRestoreMockService(t, port)
	path := filepath.Join(t.TempDir(), "backup_20260101_000000.db")
	if err := os.WriteFile(path, append([]byte("SQLite format 3\x00"), make([]byte, 64)...), 0o600); err != nil {
		t.Fatalf("write sqlite fixture: %v", err)
	}
	if _, err := svc.RestoreDatabase(path); err == nil {
		t.Fatal("RestoreDatabase accepted a SQLite snapshot on a MySQL backend")
	}
}

func TestClearDatabaseRollsBackAndReturnsDeleteError(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, mock := newRestoreMockService(t, port)
	deleteErr := errors.New("delete failed")
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM batch_skip_parts")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM batch_trace")).
		WillReturnError(deleteErr)
	mock.ExpectRollback()
	expectSessionChecksRestored(t, mock)

	if _, err := svc.ClearDatabase(); !errors.Is(err, deleteErr) {
		t.Fatalf("ClearDatabase returned the wrong error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestClearDatabaseReturnsSessionRestoreError(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, mock := newRestoreMockService(t, port)
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectCommit()
	restoreErr := errors.New("cannot restore foreign key checks")
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 1")).
		WillReturnError(restoreErr)
	mock.ExpectExec(regexp.QuoteMeta("SET UNIQUE_CHECKS = 1")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if _, err := svc.ClearDatabase(); !errors.Is(err, restoreErr) {
		t.Fatalf("ClearDatabase returned the wrong error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestClearDatabaseMarksConnectionUnusableWhenRestoreFails(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, mock := newRestoreMockService(t, port)
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 1")).
		WillReturnError(errors.New("cannot restore foreign key checks"))
	mock.ExpectExec(regexp.QuoteMeta("SET UNIQUE_CHECKS = 1")).
		WillReturnError(errors.New("cannot restore unique checks"))

	if _, err := svc.ClearDatabase(); err == nil {
		t.Fatal("ClearDatabase swallowed the session restore error")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

// TestClearDatabaseMakesPreClearCopyAndAudits 清空前必须留副本，且动作要留痕。
func TestClearDatabaseMakesPreClearCopyAndAudits(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql"}
	svc, mock := newRestoreMockService(t, port)
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectCommit()
	expectSessionChecksRestored(t, mock)
	mock.ExpectExec("INSERT INTO audit_log").WillReturnResult(sqlmock.NewResult(1, 1))

	res, err := svc.ClearDatabase()
	if err != nil {
		t.Fatalf("ClearDatabase: %v", err)
	}
	if res.PreClear == "" {
		t.Fatal("ClearDatabase did not report a pre-clear copy")
	}
	if len(port.prefixes) != 1 || port.prefixes[0] != prefixPreClear {
		t.Fatalf("safety copy prefixes = %v, want [%s]", port.prefixes, prefixPreClear)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

// TestClearDatabaseAbortsWhenSafetyCopyFails 副本失败必须中止清空。
func TestClearDatabaseAbortsWhenSafetyCopyFails(t *testing.T) {
	port := &fakeSnapshotPort{kind: "mysql", destructive: true}
	svc, mock := newRestoreMockService(t, port)

	_, err := svc.ClearDatabase()
	if err == nil {
		t.Fatal("ClearDatabase proceeded without a safety copy")
	}
	if !strings.Contains(err.Error(), "已中止清空") {
		t.Fatalf("ClearDatabase error = %v, want abort context", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("clear touched the database: %v", err)
	}
}

var _ = fmt.Sprintf
