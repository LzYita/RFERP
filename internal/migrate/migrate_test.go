package migrate

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestMigrationsIncludeQuantityValidationAfterBatchConsumptions(t *testing.T) {
	if len(migrations) == 0 {
		t.Fatal("expected migrations")
	}
	versions := make(map[string]int)
	for _, migration := range migrations {
		versions[migration.Name] = migration.Version
	}
	if versions["batch_consumptions"] != 9 {
		t.Fatalf("batch_consumptions migration version = %d, want 9", versions["batch_consumptions"])
	}
	if versions["quantity_checks"] != 10 {
		t.Fatalf("quantity_checks migration version = %d, want 10", versions["quantity_checks"])
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
	mock.ExpectExec(`(?s)INSERT INTO batch_consumptions \(batch_id, part_id, consumed_qty\).*JOIN product_batches b.*WHERE.*b\.status\s*=\s*2`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`(?s)UPDATE product_batches b.*SET consumption_recorded = 1.*WHERE EXISTS`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	var batchConsumptionMigration migration
	for _, candidate := range migrations {
		if candidate.Name == "batch_consumptions" {
			batchConsumptionMigration = candidate
			break
		}
	}
	if batchConsumptionMigration.Apply == nil {
		t.Fatal("batch_consumptions migration not found")
	}
	if err := batchConsumptionMigration.Apply(sqlx.NewDb(db, "sqlmock")); err != nil {
		t.Fatalf("apply batch consumptions migration: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestApplyQuantityChecksAddsConstraintsIdempotently(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	defer db.Close()

	checks := []struct {
		table string
		name  string
	}{
		{table: "parts", name: "ck_parts_stock_nonnegative"},
		{table: "parts", name: "ck_parts_warn_nonnegative"},
		{table: "bom_items", name: "ck_bom_quantity_positive"},
		{table: "bom_items", name: "ck_bom_loss_rate_range"},
		{table: "product_batches", name: "ck_batches_plan_positive"},
		{table: "batch_trace", name: "ck_trace_used_positive"},
	}
	for _, check := range checks {
		mock.ExpectQuery(`(?s)SELECT COUNT\(\*\) FROM information_schema\.TABLE_CONSTRAINTS.*`).
			WithArgs(check.table, check.name).
			WillReturnRows(sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(0))
		mock.ExpectExec(regexp.QuoteMeta("ALTER TABLE `" + check.table + "` ADD CONSTRAINT `" + check.name + "` CHECK (")).
			WillReturnResult(sqlmock.NewResult(0, 0))
	}

	var quantityChecksMigration migration
	for _, candidate := range migrations {
		if candidate.Name == "quantity_checks" {
			quantityChecksMigration = candidate
			break
		}
	}
	if quantityChecksMigration.Apply == nil {
		t.Fatal("quantity_checks migration not found")
	}
	if err := quantityChecksMigration.Apply(sqlx.NewDb(db, "sqlmock")); err != nil {
		t.Fatalf("apply quantity checks: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}
