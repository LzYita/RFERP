package service

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"app/internal/usecase"
)

func TestRestoreReportsUnknownWhenRollbackFails(t *testing.T) {
	svc, mock := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	cause := errors.New("rollback interrupted")
	mock.ExpectRollback().WillReturnError(cause)
	expectSessionChecksRestored(t, mock)

	_, err := svc.RestoreDatabase(writeDump(t, "INSERT INTO products (code) VALUES ('P-1')"))
	requireOperationState(t, err, usecase.OperationUnknown, cause)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClearReportsAppliedWhenSessionCleanupFails(t *testing.T) {
	svc, mock := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectCommit()
	cause := errors.New("checks cleanup interrupted")
	mock.ExpectExec("SET FOREIGN_KEY_CHECKS = 1").WillReturnError(cause)
	mock.ExpectExec("SET UNIQUE_CHECKS = 1").WillReturnResult(sqlmock.NewResult(0, 0))

	_, err := svc.ClearDatabase()
	requireOperationState(t, err, usecase.OperationApplied, cause)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
