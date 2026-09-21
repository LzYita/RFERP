package service

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRestoreDatabasePropagatesSessionSetupErrorAndRestoresChecks(t *testing.T) {
	svc, mock := newMockService(t)
	path := filepath.Join(t.TempDir(), "restore.sql")
	if err := os.WriteFile(path, []byte("INSERT INTO products (code) VALUES ('P-1');\n"), 0o600); err != nil {
		t.Fatalf("write restore fixture: %v", err)
	}

	setupErr := errors.New("cannot disable foreign key checks")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT @@FOREIGN_KEY_CHECKS AS foreign_key_checks, @@UNIQUE_CHECKS AS unique_checks")).
		WillReturnRows(sqlmock.NewRows([]string{"foreign_key_checks", "unique_checks"}).AddRow(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 0")).
		WillReturnError(setupErr)
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 1")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("SET UNIQUE_CHECKS = 1")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	_, _, err := svc.RestoreDatabase(path)
	if err == nil {
		t.Fatal("RestoreDatabase swallowed the session setup error")
	}
	if !errors.Is(err, setupErr) {
		t.Fatalf("RestoreDatabase returned the wrong error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
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

func TestRestoreDatabaseRollsBackAndReturnsInsertError(t *testing.T) {
	svc, mock := newMockService(t)
	path := filepath.Join(t.TempDir(), "restore.sql")
	if err := os.WriteFile(path, []byte("INSERT INTO products (code) VALUES ('P-1');\n"), 0o600); err != nil {
		t.Fatalf("write restore fixture: %v", err)
	}

	insertErr := errors.New("duplicate product")
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO products (code) VALUES ('P-1');")).
		WillReturnError(insertErr)
	mock.ExpectRollback()
	expectSessionChecksRestored(t, mock)

	success, failed, err := svc.RestoreDatabase(path)
	if success != 0 || failed != 1 {
		t.Fatalf("RestoreDatabase counts = (%d, %d), want (0, 1)", success, failed)
	}
	if !errors.Is(err, insertErr) {
		t.Fatalf("RestoreDatabase returned the wrong error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestClearDatabaseRollsBackAndReturnsDeleteError(t *testing.T) {
	svc, mock := newMockService(t)
	deleteErr := errors.New("delete failed")
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM batch_skip_parts")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM batch_trace")).
		WillReturnError(deleteErr)
	mock.ExpectRollback()
	expectSessionChecksRestored(t, mock)

	if err := svc.ClearDatabase(); !errors.Is(err, deleteErr) {
		t.Fatalf("ClearDatabase returned the wrong error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestClearDatabaseReturnsSessionRestoreError(t *testing.T) {
	svc, mock := newMockService(t)
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	for _, stmt := range []string{
		"DELETE FROM batch_skip_parts",
		"DELETE FROM batch_trace",
		"DELETE FROM batch_consumptions",
		"DELETE FROM bom_items",
		"DELETE FROM product_batches",
		"DELETE FROM parts",
		"DELETE FROM products",
		"DELETE FROM audit_log",
	} {
		mock.ExpectExec(regexp.QuoteMeta(stmt)).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	mock.ExpectCommit()
	restoreErr := errors.New("cannot restore foreign key checks")
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 1")).
		WillReturnError(restoreErr)
	mock.ExpectExec(regexp.QuoteMeta("SET UNIQUE_CHECKS = 1")).
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := svc.ClearDatabase(); !errors.Is(err, restoreErr) {
		t.Fatalf("ClearDatabase returned the wrong error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}
