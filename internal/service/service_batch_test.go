package service

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"app/internal/model"
	"app/internal/repository"
)

var (
	batchSelectForUpdate = regexp.QuoteMeta("SELECT * FROM product_batches WHERE id=? FOR UPDATE")
	partSelectForUpdate  = regexp.QuoteMeta("SELECT * FROM parts WHERE id=? FOR UPDATE")
	auditInsert          = regexp.QuoteMeta("INSERT INTO audit_log (table_name,record_id,action,old_data,new_data,operator) VALUES (?,?,?,?,?,?)")
	statusUpdate         = regexp.QuoteMeta("UPDATE product_batches SET status=?, operator=?, version=version+1 WHERE id=? AND status=?")
)

func newMockService(t *testing.T) (*Service, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("open sql mock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return New(repository.New(sqlx.NewDb(db, "sqlmock")), "", "", nil), mock
}

func batchRows(now time.Time, status int, produced int) *sqlmock.Rows {
	recorded := 0
	if status == 2 {
		recorded = 1
	}
	return sqlmock.NewRows([]string{
		"id", "batch_no", "product_id", "plan_qty", "produced_qty", "status", "version",
		"created_at", "updated_at", "operator", "customer", "consumption_recorded",
	}).AddRow(1, "B-1", 10, 4, produced, status, 1, now, now, "old", nil, recorded)
}

func batchRowsWithConsumptionRecorded(now time.Time, status, produced, recorded int) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "batch_no", "product_id", "plan_qty", "produced_qty", "status", "version",
		"created_at", "updated_at", "operator", "customer", "consumption_recorded",
	}).AddRow(1, "B-1", 10, 4, produced, status, 1, now, now, "old", nil, recorded)
}

func partRows(now time.Time, stock float64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "code", "name", "spec", "unit", "part_type", "stock_qty", "warn_qty", "status", "version",
		"created_at", "updated_at", "operator", "supplier",
	}).AddRow(20, "P-20", "Part 20", nil, "个", nil, stock, 0, 1, 1, now, now, "old", nil)
}

func productRows(now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "code", "name", "spec", "unit", "status", "version", "created_at", "updated_at", "operator",
	}).AddRow(10, "PR-10", "Product 10", nil, "个", 1, 1, now, now, "old")
}

func bomRows(now time.Time, quantity float64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "product_id", "part_id", "quantity", "loss_rate", "remark", "version",
		"created_at", "updated_at", "operator", "replaceable", "use_mode", "part_code", "part_name",
	}).AddRow(1, 10, 20, quantity, 0, nil, 1, now, now, "old", 0, 0, "P-20", "Part 20")
}

func bomRowsWithLoss(now time.Time, quantity, lossRate float64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "product_id", "part_id", "quantity", "loss_rate", "remark", "version",
		"created_at", "updated_at", "operator", "replaceable", "use_mode", "part_code", "part_name",
	}).AddRow(1, 10, 20, quantity, lossRate, nil, 1, now, now, "old", 0, 0, "P-20", "Part 20")
}

func auditJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal audit value: %v", err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		t.Fatalf("normalize audit value: %v", err)
	}
	raw, err = json.Marshal(normalized)
	if err != nil {
		t.Fatalf("marshal normalized audit value: %v", err)
	}
	return raw
}

func expectPartAudit(t *testing.T, mock sqlmock.Sqlmock, old *model.Part, action string, newData map[string]any, operator string) {
	t.Helper()
	mock.ExpectExec(auditInsert).
		WithArgs("parts", int64(20), action, auditJSON(t, old), auditJSON(t, newData), operator).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

func expectBatchAudit(t *testing.T, mock sqlmock.Sqlmock, old any, newData map[string]any, action string, operator string) {
	t.Helper()
	mock.ExpectExec(auditInsert).
		WithArgs("product_batches", int64(1), action, auditJSON(t, old), auditJSON(t, newData), operator).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

func TestUpdateBatchStatusRejectsIllegalTransitions(t *testing.T) {
	tests := []struct {
		name        string
		fromStatus  int
		toStatus    int
		expectError string
	}{
		{name: "completed_to_pending", fromStatus: 2, toStatus: 0, expectError: "已完成"},
		{name: "completed_to_in_progress", fromStatus: 2, toStatus: 1, expectError: "已完成"},
		{name: "completed_to_paused", fromStatus: 2, toStatus: 3, expectError: "已完成"},
		{name: "direct_revoke", fromStatus: 1, toStatus: 4, expectError: "RevokeBatch"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mock := newMockService(t)
			now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

			mock.ExpectBegin()
			mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, tt.fromStatus, 4))
			mock.ExpectRollback()

			err := svc.UpdateBatchStatus(1, tt.toStatus, "operator")
			if err == nil || !strings.Contains(err.Error(), tt.expectError) {
				t.Fatalf("expected illegal transition error containing %q, got %v", tt.expectError, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("database expectations: %v", err)
			}
		})
	}
}

func TestCompleteBatchRecordsActualConsumptionAndCommits(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldPart := &model.Part{ID: 20, Code: "P-20", Name: "Part 20", Unit: "个", StockQty: 3, WarnQty: 0, Status: 1, Version: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 1, 0))
	mock.ExpectQuery(`(?s)SELECT b\.\*, p\.code AS part_code, p\.name AS part_name.*FROM bom_items`).
		WithArgs(int64(10)).WillReturnRows(bomRows(now, 2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT part_id FROM batch_skip_parts WHERE batch_id=?")).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"part_id"}))
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 3))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1 WHERE id=?")).
		WithArgs(float64(0), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO batch_consumptions (batch_id,part_id,consumed_qty) VALUES (?,?,?)")).
		WithArgs(int64(1), int64(20), float64(3)).WillReturnResult(sqlmock.NewResult(1, 1))
	expectPartAudit(t, mock, oldPart, "STOCK_DEDUCT", map[string]any{
		"old_stock":        float64(3),
		"requested_deduct": float64(8),
		"deduct":           float64(3),
		"new_stock":        float64(0),
		"batch_id":         int64(1),
	}, "operator")
	mock.ExpectExec(regexp.QuoteMeta("UPDATE product_batches SET produced_qty=? WHERE id=?")).
		WithArgs(4, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE product_batches SET consumption_recorded=1 WHERE id=?")).
		WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM products WHERE id=?")).
		WithArgs(int64(10)).WillReturnRows(productRows(now))
	mock.ExpectExec(statusUpdate).WithArgs(2, "operator", int64(1), 1).WillReturnResult(sqlmock.NewResult(0, 1))
	expectBatchAudit(t, mock, map[string]any{
		"batch_no":     "B-1",
		"product_id":   int64(10),
		"product_code": "PR-10",
		"product_name": "Product 10",
		"plan_qty":     4,
		"status":       1,
		"customer":     "",
	}, map[string]any{"status": 2}, "UPDATE_STATUS", "operator")
	mock.ExpectCommit()

	if err := svc.UpdateBatchStatus(1, 2, "operator"); err != nil {
		t.Fatalf("complete batch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestCompleteBatchQuantizesComputedConsumptionBeforePersistence(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldPart := &model.Part{ID: 20, Code: "P-20", Name: "Part 20", Unit: "个", StockQty: 1, WarnQty: 0, Status: 1, Version: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}
	batchRowsWithPlan := sqlmock.NewRows([]string{
		"id", "batch_no", "product_id", "plan_qty", "produced_qty", "status", "version",
		"created_at", "updated_at", "operator", "customer", "consumption_recorded",
	}).AddRow(1, "B-1", 10, 1, 0, 1, 1, now, now, "old", nil, 0)

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRowsWithPlan)
	mock.ExpectQuery(`(?s)SELECT b\.\*, p\.code AS part_code, p\.name AS part_name.*FROM bom_items`).
		WithArgs(int64(10)).WillReturnRows(bomRowsWithLoss(now, 0.01, 50))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT part_id FROM batch_skip_parts WHERE batch_id=?")).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"part_id"}))
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1 WHERE id=?")).
		WithArgs(float64(0.98), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO batch_consumptions (batch_id,part_id,consumed_qty) VALUES (?,?,?)")).
		WithArgs(int64(1), int64(20), float64(0.02)).WillReturnResult(sqlmock.NewResult(1, 1))
	expectPartAudit(t, mock, oldPart, "STOCK_DEDUCT", map[string]any{
		"old_stock":        float64(1),
		"requested_deduct": float64(0.02),
		"deduct":           float64(0.02),
		"new_stock":        float64(0.98),
		"batch_id":         int64(1),
	}, "operator")
	mock.ExpectExec(regexp.QuoteMeta("UPDATE product_batches SET produced_qty=? WHERE id=?")).
		WithArgs(1, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE product_batches SET consumption_recorded=1 WHERE id=?")).
		WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM products WHERE id=?")).
		WithArgs(int64(10)).WillReturnRows(productRows(now))
	mock.ExpectExec(statusUpdate).WithArgs(2, "operator", int64(1), 1).WillReturnResult(sqlmock.NewResult(0, 1))
	expectBatchAudit(t, mock, map[string]any{
		"batch_no":     "B-1",
		"product_id":   int64(10),
		"product_code": "PR-10",
		"product_name": "Product 10",
		"plan_qty":     1,
		"status":       1,
		"customer":     "",
	}, map[string]any{"status": 2}, "UPDATE_STATUS", "operator")
	mock.ExpectCommit()

	if err := svc.UpdateBatchStatus(1, 2, "operator"); err != nil {
		t.Fatalf("complete batch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestCompleteBatchRollsBackStockAndConsumptionWhenAuditFails(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 1, 0))
	mock.ExpectQuery(`(?s)SELECT b\.\*, p\.code AS part_code, p\.name AS part_name.*FROM bom_items`).
		WithArgs(int64(10)).WillReturnRows(bomRows(now, 2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT part_id FROM batch_skip_parts WHERE batch_id=?")).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"part_id"}))
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 3))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1 WHERE id=?")).
		WithArgs(float64(0), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO batch_consumptions (batch_id,part_id,consumed_qty) VALUES (?,?,?)")).
		WithArgs(int64(1), int64(20), float64(3)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(auditInsert).WillReturnError(errors.New("stock audit failed"))
	mock.ExpectRollback()

	err := svc.UpdateBatchStatus(1, 2, "operator")
	if err == nil || !strings.Contains(err.Error(), "stock audit failed") {
		t.Fatalf("expected stock audit error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestRevokeBatchReversesRecordedActualConsumptionAndAuditsStatusFour(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldPart := &model.Part{ID: 20, Code: "P-20", Name: "Part 20", Unit: "个", StockQty: 1, WarnQty: 0, Status: 1, Version: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}
	oldBatch := &model.ProductBatch{ID: 1, BatchNo: "B-1", ProductID: 10, PlanQty: 4, ProducedQty: 4, Status: 2, Version: 1, ConsumptionRecorded: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 2, 4))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT part_id, consumed_qty FROM batch_consumptions WHERE batch_id=? ORDER BY part_id")).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"part_id", "consumed_qty"}).AddRow(20, 3.0))
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1 WHERE id=?")).
		WithArgs(float64(4), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectPartAudit(t, mock, oldPart, "STOCK_ADJUST", map[string]any{
		"old_stock": float64(1),
		"new_stock": float64(4),
		"diff":      float64(3),
		"batch_id":  int64(1),
		"remark":    "批次撤销回退",
	}, "operator")
	mock.ExpectExec(statusUpdate).WithArgs(4, "operator", int64(1), 2).WillReturnResult(sqlmock.NewResult(0, 1))
	expectBatchAudit(t, mock, oldBatch, map[string]any{
		"batch_no":     "B-1",
		"product_id":   int64(10),
		"plan_qty":     4,
		"produced_qty": 4,
		"status":       4,
		"customer":     nil,
		"operator":     "operator",
	}, "REVOKE", "operator")
	mock.ExpectCommit()

	if err := svc.RevokeBatch(1, "operator"); err != nil {
		t.Fatalf("revoke batch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestRevokeBatchDoesNotInventStockForZeroActualConsumption(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldBatch := &model.ProductBatch{ID: 1, BatchNo: "B-1", ProductID: 10, PlanQty: 4, ProducedQty: 4, Status: 2, Version: 1, ConsumptionRecorded: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 2, 4))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT part_id, consumed_qty FROM batch_consumptions WHERE batch_id=? ORDER BY part_id")).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"part_id", "consumed_qty"}).AddRow(20, 0.0))
	mock.ExpectExec(statusUpdate).WithArgs(4, "operator", int64(1), 2).WillReturnResult(sqlmock.NewResult(0, 1))
	expectBatchAudit(t, mock, oldBatch, map[string]any{
		"batch_no":     "B-1",
		"product_id":   int64(10),
		"plan_qty":     4,
		"produced_qty": 4,
		"status":       4,
		"customer":     nil,
		"operator":     "operator",
	}, "REVOKE", "operator")
	mock.ExpectCommit()

	if err := svc.RevokeBatch(1, "operator"); err != nil {
		t.Fatalf("revoke batch with zero actual consumption: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestRevokeBatchRejectsCompletedBatchWithoutFrozenConsumptionRecord(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).
		WillReturnRows(batchRowsWithConsumptionRecorded(now, 2, 4, 0))
	mock.ExpectRollback()

	err := svc.RevokeBatch(1, "operator")
	if err == nil || !strings.Contains(err.Error(), "冻结") {
		t.Fatalf("expected missing frozen consumption error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestStockInUsesLockedTransactionalReadModifyWrite(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldPart := &model.Part{ID: 20, Code: "P-20", Name: "Part 20", Unit: "个", StockQty: 3, WarnQty: 0, Status: 1, Version: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}

	mock.ExpectBegin()
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 3))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1 WHERE id=?")).
		WithArgs(float64(5), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectPartAudit(t, mock, oldPart, "STOCK_IN", map[string]any{
		"old_stock": float64(3),
		"in_qty":    float64(2),
		"new_stock": float64(5),
	}, "operator")
	mock.ExpectCommit()

	if err := svc.StockIn(20, 2, "operator"); err != nil {
		t.Fatalf("stock in: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestAdjustStockUsesLockedTransactionalReadModifyWrite(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldPart := &model.Part{ID: 20, Code: "P-20", Name: "Part 20", Unit: "个", StockQty: 3, WarnQty: 0, Status: 1, Version: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}

	mock.ExpectBegin()
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 3))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1 WHERE id=?")).
		WithArgs(float64(8), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectPartAudit(t, mock, oldPart, "STOCK_ADJUST", map[string]any{
		"old_stock": float64(3),
		"new_stock": float64(8),
		"diff":      float64(5),
	}, "operator")
	mock.ExpectCommit()

	if err := svc.AdjustStock(20, 8, "operator"); err != nil {
		t.Fatalf("adjust stock: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestSkipPartChangesLockBatchAndRejectAfterCompletionStarts(t *testing.T) {
	tests := []struct {
		name string
		call func(*Service) error
	}{
		{name: "add", call: func(s *Service) error { return s.AddSkipPart(1, 20) }},
		{name: "remove", call: func(s *Service) error { return s.RemoveSkipPart(1, 20) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, mock := newMockService(t)
			now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

			mock.ExpectBegin()
			mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 2, 4))
			mock.ExpectRollback()

			err := tt.call(svc)
			if err == nil || !strings.Contains(err.Error(), "完成") {
				t.Fatalf("expected completed batch rejection, got %v", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("database expectations: %v", err)
			}
		})
	}
}

func TestAddSkipPartCommitsAfterLockingMutableBatch(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 1, 0))
	mock.ExpectExec(regexp.QuoteMeta("INSERT IGNORE INTO batch_skip_parts (batch_id,part_id) VALUES (?,?)")).
		WithArgs(int64(1), int64(20)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	if err := svc.AddSkipPart(1, 20); err != nil {
		t.Fatalf("add skip part: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestRemoveSkipPartCommitsAfterLockingMutableBatch(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 1, 0))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM batch_skip_parts WHERE batch_id=? AND part_id=?")).
		WithArgs(int64(1), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	if err := svc.RemoveSkipPart(1, 20); err != nil {
		t.Fatalf("remove skip part: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func stringPtr(value string) *string { return &value }
