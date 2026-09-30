package service

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"app/internal/usecase"
)

func requireOperationState(t *testing.T, err error, state usecase.OperationState, cause error) {
	t.Helper()
	var outcome *usecase.OperationError
	if !errors.As(err, &outcome) || outcome.State != state || !errors.Is(err, cause) {
		t.Fatalf("operation error = %v, want state %s with cause %v", err, state, cause)
	}
}

func TestRestoreReportsAppliedWhenSessionCleanupFails(t *testing.T) {
	svc, mock := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectCommit()
	cause := errors.New("restore checks failed")
	mock.ExpectExec(regexp.QuoteMeta("SET FOREIGN_KEY_CHECKS = 1")).WillReturnError(cause)
	mock.ExpectExec(regexp.QuoteMeta("SET UNIQUE_CHECKS = 1")).WillReturnResult(sqlmock.NewResult(0, 0))

	res, err := svc.RestoreDatabase(writeDump(t, ""))
	requireOperationState(t, err, usecase.OperationApplied, cause)
	if res.PreRestore == "" {
		t.Fatal("lost safety copy path")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreReportsAppliedWhenVerificationFails(t *testing.T) {
	svc, mock := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectCommit()
	expectSessionChecksRestored(t, mock)
	mock.ExpectBegin()
	cause := errors.New("count unavailable")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM " + businessTables[0])).WillReturnError(cause)
	mock.ExpectRollback()

	_, err := svc.RestoreDatabase(writeDump(t, ""))
	requireOperationState(t, err, usecase.OperationApplied, cause)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreReportsAppliedWhenAuditFails(t *testing.T) {
	svc, mock := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectCommit()
	expectSessionChecksRestored(t, mock)
	mock.ExpectBegin()
	for _, table := range businessTables {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM " + table)).
			WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))
	}
	mock.ExpectCommit()
	cause := errors.New("audit unavailable")
	mock.ExpectExec("INSERT INTO audit_log").WillReturnError(cause)

	_, err := svc.RestoreDatabase(writeDump(t, ""))
	requireOperationState(t, err, usecase.OperationApplied, cause)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreReportsUnknownWhenCommitFails(t *testing.T) {
	svc, mock := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	cause := errors.New("connection lost during commit")
	mock.ExpectCommit().WillReturnError(cause)
	expectSessionChecksRestored(t, mock)

	_, err := svc.RestoreDatabase(writeDump(t, ""))
	requireOperationState(t, err, usecase.OperationUnknown, cause)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestClearReportsAppliedWhenAuditFails(t *testing.T) {
	svc, mock := newRestoreMockService(t, &fakeSnapshotPort{kind: "mysql"})
	expectSessionChecks(t, mock)
	mock.ExpectBegin()
	expectBusinessDeletes(t, mock)
	mock.ExpectCommit()
	expectSessionChecksRestored(t, mock)
	cause := errors.New("audit unavailable")
	mock.ExpectExec("INSERT INTO audit_log").WillReturnError(cause)

	_, err := svc.ClearDatabase()
	requireOperationState(t, err, usecase.OperationApplied, cause)
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
