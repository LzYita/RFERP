package migrate

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestMigrationsIncludeVersionNineForBatchConsumptions(t *testing.T) {
	if len(migrations) == 0 {
		t.Fatal("expected migrations")
	}
	last := migrations[len(migrations)-1]
	if last.Version != 9 {
		t.Fatalf("last migration version = %d, want 9", last.Version)
	}
	if last.Name != "batch_consumptions" {
		t.Fatalf("last migration name = %q, want batch_consumptions", last.Name)
	}
}

func TestApplyBatchConsumptionsCreatesIdempotentTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`(?s)SELECT COUNT\(\*\) FROM information_schema\.COLUMNS.*`).
		WithArgs("product_batches", "consumption_recorded").
		WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(0))
	mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE product_batches ADD COLUMN consumption_recorded")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("CREATE TABLE IF NOT EXISTS batch_consumptions (")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO batch_consumptions (batch_id, part_id, consumed_qty)")).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s)UPDATE product_batches b.*SET consumption_recorded = 1.*WHERE EXISTS`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	last := migrations[len(migrations)-1]
	if err := last.Apply(sqlx.NewDb(db, "sqlmock")); err != nil {
		t.Fatalf("apply batch consumptions migration: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}
