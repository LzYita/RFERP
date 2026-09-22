package repository

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestWithConnUsesCheckedOutConnectionAndReturnsCallbackError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })

	callbackErr := errors.New("callback failed")
	mock.ExpectExec("SELECT 1").WillReturnResult(sqlmock.NewResult(0, 0))

	repo := New(sqlx.NewDb(db, "sqlmock"))
	err = repo.WithConn(func(conn *Conn) error {
		if _, err := conn.Exec("SELECT 1"); err != nil {
			return err
		}
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("WithConn returned the wrong error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestWithConnMarkUnusableDiscardsConnection(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = db.Close() })

	mock.ExpectExec("SELECT 1").WillReturnResult(sqlmock.NewResult(0, 0))

	repo := New(sqlx.NewDb(db, "sqlmock"))
	err = repo.WithConn(func(conn *Conn) error {
		if _, err := conn.Exec("SELECT 1"); err != nil {
			return err
		}
		conn.MarkUnusable()
		return nil
	})
	if err != nil {
		t.Fatalf("WithConn returned an unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}

	// A discarded connection must not satisfy a later checkout silently.
	mock.ExpectExec("SELECT 1").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := repo.WithConn(func(conn *Conn) error {
		_, err := conn.Exec("SELECT 1")
		return err
	}); err != nil {
		t.Fatalf("WithConn after discard: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations after discard: %v", err)
	}
}
