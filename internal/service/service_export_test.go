package service

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestExportAllDataCSVReturnsQueryErrorAndRemovesIncompleteDirectory(t *testing.T) {
	svc, mock := newMockService(t)
	saveDir := t.TempDir()
	queryErr := errors.New("products query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM products ORDER BY id")).
		WillReturnError(queryErr)

	files, subDir, err := svc.ExportAllDataCSV(saveDir)
	if !errors.Is(err, queryErr) {
		t.Fatalf("ExportAllDataCSV returned the wrong error: %v", err)
	}
	if files != nil || subDir != "" {
		t.Fatalf("ExportAllDataCSV returned output on failure: files=%v dir=%q", files, subDir)
	}
	entries, readErr := os.ReadDir(saveDir)
	if readErr != nil {
		t.Fatalf("read export parent: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("incomplete export directory was not removed: %s", filepath.Join(saveDir, entries[0].Name()))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestExportAllDataCSVQueriesBatchConsumptions(t *testing.T) {
	svc, mock := newMockService(t)
	saveDir := t.TempDir()
	queryErr := errors.New("batch consumptions query failed")

	emptyProducts := sqlmock.NewRows([]string{
		"id", "code", "name", "spec", "unit", "status", "version", "created_at", "updated_at", "operator",
	})
	emptyParts := sqlmock.NewRows([]string{
		"id", "code", "name", "spec", "unit", "part_type", "stock_qty", "warn_qty", "status", "version",
		"created_at", "updated_at", "operator", "supplier",
	})
	emptyBatches := sqlmock.NewRows([]string{
		"id", "batch_no", "product_id", "plan_qty", "produced_qty", "status", "version",
		"created_at", "updated_at", "operator", "customer", "consumption_recorded",
		"product_name", "product_code",
	})
	emptyBOM := sqlmock.NewRows([]string{
		"id", "product_id", "part_id", "quantity", "loss_rate", "remark", "version",
		"created_at", "updated_at", "operator", "replaceable", "use_mode", "part_code", "part_name",
	})

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM products ORDER BY id")).WillReturnRows(emptyProducts)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM parts ORDER BY id")).WillReturnRows(emptyParts)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT b.*, p.name AS product_name, p.code AS product_code
		FROM product_batches b
		JOIN products p ON p.id = b.product_id
		ORDER BY b.id DESC`)).
		WillReturnRows(emptyBatches)
	mock.ExpectQuery(regexp.QuoteMeta(`
		SELECT b.*, p.code AS part_code, p.name AS part_name
		FROM bom_items b
		JOIN parts p ON p.id = b.part_id
		ORDER BY b.product_id, b.id`)).
		WillReturnRows(emptyBOM)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT batch_id, part_id, consumed_qty FROM batch_consumptions ORDER BY batch_id, part_id")).
		WillReturnError(queryErr)

	files, subDir, err := svc.ExportAllDataCSV(saveDir)
	if !errors.Is(err, queryErr) {
		t.Fatalf("ExportAllDataCSV returned the wrong error: %v", err)
	}
	if files != nil || subDir != "" {
		t.Fatalf("ExportAllDataCSV returned output on failure: files=%v dir=%q", files, subDir)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestExportAuditLogCSVReturnsQueryError(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "audit.csv")
	queryErr := errors.New("audit query failed")
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM audit_log WHERE created_at >= ? AND created_at < ? ORDER BY id DESC")).
		WithArgs(start, end.Add(24*time.Hour)).
		WillReturnError(queryErr)

	count, err := svc.ExportAuditLogCSV(start, end, path)
	if count != 0 || !errors.Is(err, queryErr) {
		t.Fatalf("ExportAuditLogCSV = (%d, %v), want (0, query error)", count, err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("audit output exists after query failure: %v", statErr)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestExportAuditLogCSVReturnsFileCreationError(t *testing.T) {
	svc, mock := newMockService(t)
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC)
	path := t.TempDir()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM audit_log WHERE created_at >= ? AND created_at < ? ORDER BY id DESC")).
		WithArgs(start, end.Add(24*time.Hour)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "table_name", "record_id", "action", "old_data", "new_data", "operator", "created_at",
		}))

	count, err := svc.ExportAuditLogCSV(start, end, path)
	if count != 0 || err == nil {
		t.Fatalf("ExportAuditLogCSV = (%d, %v), want file creation error", count, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}
