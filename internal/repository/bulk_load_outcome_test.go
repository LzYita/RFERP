package repository

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestSQLiteBulkLoadReportsAppliedWhenSessionCleanupFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec(regexp.QuoteMeta("PRAGMA defer_foreign_keys = ON")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("BEGIN IMMEDIATE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("COMMIT").WillReturnResult(sqlmock.NewResult(0, 0))
	cause := errors.New("pragma cleanup failed")
	mock.ExpectExec(regexp.QuoteMeta("PRAGMA defer_foreign_keys = OFF")).WillReturnError(cause)

	err = NewSQLite(sqlx.NewDb(db, "sqlmock")).WithBulkLoad(func(TxOps) error { return nil })
	var outcome *BulkLoadError
	if !errors.As(err, &outcome) || outcome.Outcome != BulkLoadApplied || !errors.Is(err, cause) {
		t.Fatalf("cleanup outcome = %v, want applied with cause", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteBulkLoadReportsUnknownWhenRollbackFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec(regexp.QuoteMeta("PRAGMA defer_foreign_keys = ON")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("BEGIN IMMEDIATE").WillReturnResult(sqlmock.NewResult(0, 0))
	cause := errors.New("rollback unavailable")
	mock.ExpectExec("ROLLBACK").WillReturnError(cause)
	mock.ExpectExec(regexp.QuoteMeta("PRAGMA defer_foreign_keys = OFF")).WillReturnResult(sqlmock.NewResult(0, 0))

	err = NewSQLite(sqlx.NewDb(db, "sqlmock")).WithBulkLoad(func(TxOps) error { return errors.New("operation failed") })
	var outcome *BulkLoadError
	if !errors.As(err, &outcome) || outcome.Outcome != BulkLoadUnknown || !errors.Is(err, cause) {
		t.Fatalf("rollback outcome = %v, want unknown with cause", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
