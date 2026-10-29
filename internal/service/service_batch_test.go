package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"app/internal/model"
	"app/internal/repository"
	"app/internal/usecase"
)

var (
	batchSelectForUpdate = regexp.QuoteMeta("SELECT * FROM product_batches WHERE id=? FOR UPDATE")
	partSelectForUpdate  = regexp.QuoteMeta("SELECT * FROM parts WHERE id=? FOR UPDATE")
	auditInsert          = regexp.QuoteMeta("INSERT INTO audit_log (table_name,record_id,action,old_data,new_data,operator) VALUES (?,?,?,?,?,?)")
	statusUpdate         = regexp.QuoteMeta("UPDATE product_batches SET status=?, operator=?, version=version+1, updated_at=NOW() WHERE id=? AND status=?")
	// v17：扣料/撤销/盘点都要读写入库批次剩余。sqlmock 会把空白折叠成单个空格。
	inboundSelectForUpdate = regexp.QuoteMeta("SELECT id, part_id, batch_no, qty, COALESCE(remaining, 0) AS remaining, note, created_at FROM part_inbounds WHERE part_id = ? ORDER BY created_at, id FOR UPDATE")
	inboundUpdateRemain    = regexp.QuoteMeta("UPDATE part_inbounds SET remaining = ? WHERE id = ?")
	drawInsert             = regexp.QuoteMeta("INSERT INTO part_inbound_draws (batch_id, inbound_id, qty) VALUES (?, ?, ?)")
	drawSelectByBatch      = regexp.QuoteMeta("SELECT inbound_id, SUM(qty) AS qty FROM part_inbound_draws WHERE batch_id = ? GROUP BY inbound_id")
	drawDeleteByBatch      = regexp.QuoteMeta("DELETE FROM part_inbound_draws WHERE batch_id = ?")
	inboundRestore         = regexp.QuoteMeta("UPDATE part_inbounds SET remaining = COALESCE(remaining, 0) + ? WHERE id = ?")
	inboundInsert          = regexp.QuoteMeta("INSERT INTO part_inbounds (part_id, qty, remaining, note) VALUES (?, ?, ?, ?)")
	inboundSetBatchNo      = regexp.QuoteMeta("UPDATE part_inbounds SET batch_no = ? WHERE id = ?")
	// v18：完成生产先读本批投料计划里的批次顺序，再按顺序扣。
	allocSelectForBatch = regexp.QuoteMeta("SELECT inbound_id AS inbound_id, qty AS qty FROM batch_trace_allocs WHERE batch_id = ? AND part_id = ? ORDER BY position, id")
)

// inboundRows 造一笔入库批次行；remaining 默认等于 qty。
func inboundRows(now time.Time, id int64, qty, remaining float64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "part_id", "batch_no", "qty", "remaining", "note", "created_at"}).
		AddRow(id, 20, "RK-20260101-0001", qty, remaining, nil, now)
}

// expectDrawFIFO 登记"完成生产时按先进先出扣批次"的 SQL。
// 先查本批的分配顺序（这里没有计划，返回空 → 走先进先出兜底）。
func expectDrawFIFO(t *testing.T, mock sqlmock.Sqlmock, now time.Time, inboundID int64, qty, remaining, take float64) {
	t.Helper()
	mock.ExpectQuery(allocSelectForBatch).WithArgs(int64(1), int64(20)).
		WillReturnRows(sqlmock.NewRows([]string{"inbound_id", "qty"}))
	mock.ExpectQuery(inboundSelectForUpdate).WithArgs(int64(20)).
		WillReturnRows(inboundRows(now, inboundID, qty, remaining))
	mock.ExpectExec(inboundUpdateRemain).WithArgs(remaining-take, inboundID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(drawInsert).WithArgs(int64(1), inboundID, take).
		WillReturnResult(sqlmock.NewResult(1, 1))
}

// expectRestoreDraws 登记"撤销生产批次时按原批次还回"的三条 SQL。
func expectRestoreDraws(t *testing.T, mock sqlmock.Sqlmock, inboundID int64, qty float64) {
	t.Helper()
	rows := sqlmock.NewRows([]string{"inbound_id", "qty"}).AddRow(inboundID, qty)
	mock.ExpectQuery(drawSelectByBatch).WithArgs(int64(1)).WillReturnRows(rows)
	mock.ExpectExec(inboundRestore).WithArgs(qty, inboundID).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(drawDeleteByBatch).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
}

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

			err := svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: 1, Status: tt.toStatus, Operator: "operator"})
			if err == nil || !strings.Contains(err.Error(), tt.expectError) {
				t.Fatalf("expected illegal transition error containing %q, got %v", tt.expectError, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatalf("database expectations: %v", err)
			}
		})
	}
}

// 决策 B（2026-10-13 用户定）：库存不足时**拒绝完成生产**，而不是扣到 0。
//
// 旧行为是 actualDeduct = min(请求, 现有)：库存不够也把批次标记成已完成、接口返回
// 成功，账面库存归零但批次齐套，账实不符且调用方毫无察觉。现在缺料即报错中止
// （与创建批次时的缺料拦截同一语义），并且要**一次列全**缺料项。
//
// 断言的是「没有任何写入发生」+ 错误里点名缺料零件：只断言返回错误不够，
// 因为若实现先扣了料再报错，回滚路径也可能掩盖问题——必须确认一个
// UpdatePartStock / CreateBatchConsumption 都没有被调用（mock 未设期望即会失败）。
// 库存充足时的完成路径：BOM 要求扣多少就扣多少，消耗记录与审计都按实扣数写。
// （决策 B 之前，这里没有独立的成功用例——原本的"成功"用例其实是缺料夹取。）
func TestCompleteBatchRecordsActualConsumptionAndCommits(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldPart := &model.Part{ID: 20, Code: "P-20", Name: "Part 20", Unit: "个", StockQty: 20, WarnQty: 0, Status: 1, Version: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 1, 0))
	// plan 4 × qty 2 = 需要 8；库存 20，充足
	mock.ExpectQuery(`(?s)SELECT b\.\*, p\.code AS part_code, p\.name AS part_name.*FROM bom_items`).
		WithArgs(int64(10)).WillReturnRows(bomRows(now, 2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT part_id FROM batch_skip_parts WHERE batch_id=?")).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"part_id"}))
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 20))
	mock.ExpectQuery(`(?s)FROM batch_temp_parts.*WHERE t\.batch_id=\?`).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"batch_id", "part_id", "qty", "created_at", "code", "name", "unit"}))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1, updated_at=NOW() WHERE id=?")).
		WithArgs(float64(12), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	// 批次剩余同步扣：入库 20，扣 8，剩余 12。
	expectDrawFIFO(t, mock, now, 5, 20, 20, 8)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO batch_consumptions (batch_id,part_id,consumed_qty) VALUES (?,?,?)")).
		WithArgs(int64(1), int64(20), float64(8)).WillReturnResult(sqlmock.NewResult(1, 1))
	// 实扣数与请求数现在必然相等（缺料即中止），仍记两列是为了让审计读者
	// 能一眼确认"没有发生静默少扣"。
	expectPartAudit(t, mock, oldPart, "STOCK_DEDUCT", map[string]any{
		"old_stock":        float64(20),
		"requested_deduct": float64(8),
		"deduct":           float64(8),
		"new_stock":        float64(12),
		"batch_id":         int64(1),
		"source":           "bom",
	}, "operator")
	mock.ExpectExec(regexp.QuoteMeta("UPDATE product_batches SET produced_qty=?, updated_at=NOW() WHERE id=?")).
		WithArgs(4, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE product_batches SET consumption_recorded=1, updated_at=NOW() WHERE id=?")).
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

	if err := svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: 1, Status: 2, Operator: "operator"}); err != nil {
		t.Fatalf("complete batch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestCompleteBatchRejectsWhenStockIsInsufficient(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

	mock.ExpectBegin()
	// plan 4 × qty 2 = 需要 8；库存只有 3
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 1, 0))
	mock.ExpectQuery(`(?s)SELECT b\.\*, p\.code AS part_code, p\.name AS part_name.*FROM bom_items`).
		WithArgs(int64(10)).WillReturnRows(bomRows(now, 2))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT part_id FROM batch_skip_parts WHERE batch_id=?")).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"part_id"}))
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 3))
	mock.ExpectQuery(`(?s)FROM batch_temp_parts.*WHERE t\.batch_id=\?`).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"batch_id", "part_id", "qty", "created_at", "code", "name", "unit"}))
	// 缺料即中止：不设任何 UpdatePartStock / CreateBatchConsumption / 批次更新期望
	mock.ExpectRollback()

	err := svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: 1, Status: 2, Operator: "operator"})
	if err == nil {
		t.Fatal("库存不足却返回成功——批次会被标记完成、账实不符")
	}
	msg := err.Error()
	for _, want := range []string{"库存不足", "Part 20", "P-20", "需要 8", "当前 3"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息缺少 %q，实际：%q", want, msg)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("缺料时不应有任何扣减写入：%v", err)
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
	mock.ExpectQuery(`(?s)FROM batch_temp_parts.*WHERE t\.batch_id=\?`).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"batch_id", "part_id", "qty", "created_at", "code", "name", "unit"}))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1, updated_at=NOW() WHERE id=?")).
		WithArgs(float64(0.98), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectDrawFIFO(t, mock, now, 5, 1, 1, 0.02)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO batch_consumptions (batch_id,part_id,consumed_qty) VALUES (?,?,?)")).
		WithArgs(int64(1), int64(20), float64(0.02)).WillReturnResult(sqlmock.NewResult(1, 1))
	expectPartAudit(t, mock, oldPart, "STOCK_DEDUCT", map[string]any{
		"old_stock":        float64(1),
		"requested_deduct": float64(0.02),
		"deduct":           float64(0.02),
		"new_stock":        float64(0.98),
		"batch_id":         int64(1),
		"source":           "bom",
	}, "operator")
	mock.ExpectExec(regexp.QuoteMeta("UPDATE product_batches SET produced_qty=?, updated_at=NOW() WHERE id=?")).
		WithArgs(1, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE product_batches SET consumption_recorded=1, updated_at=NOW() WHERE id=?")).
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

	if err := svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: 1, Status: 2, Operator: "operator"}); err != nil {
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
	// 本用例要测的是「审计写失败时扣减必须回滚」，不是缺料拦截（那是另一个用例）。
	// 给足库存（plan 4 × qty 2 = 需要 8，库存 100）让流程走完扣减再在审计处失败。
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 100))
	mock.ExpectQuery(`(?s)FROM batch_temp_parts.*WHERE t\.batch_id=\?`).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"batch_id", "part_id", "qty", "created_at", "code", "name", "unit"}))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1, updated_at=NOW() WHERE id=?")).
		WithArgs(float64(92), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectDrawFIFO(t, mock, now, 5, 100, 100, 8)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO batch_consumptions (batch_id,part_id,consumed_qty) VALUES (?,?,?)")).
		WithArgs(int64(1), int64(20), float64(8)).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(auditInsert).WillReturnError(errors.New("stock audit failed"))
	mock.ExpectRollback()

	err := svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: 1, Status: 2, Operator: "operator"})
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
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1, updated_at=NOW() WHERE id=?")).
		WithArgs(float64(4), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectRestoreDraws(t, mock, 5, 3)
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

	if err := svc.RevokeBatch(usecase.RevokeBatchInput{ID: 1, Operator: "operator"}); err != nil {
		t.Fatalf("revoke batch: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestRevokeBatchQuantizesRestoredStockBeforePersistence(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldPart := &model.Part{ID: 20, Code: "P-20", Name: "Part 20", Unit: "个", StockQty: 0.1, WarnQty: 0, Status: 1, Version: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}
	oldBatch := &model.ProductBatch{ID: 1, BatchNo: "B-1", ProductID: 10, PlanQty: 4, ProducedQty: 4, Status: 2, Version: 1, ConsumptionRecorded: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}

	mock.ExpectBegin()
	mock.ExpectQuery(batchSelectForUpdate).WithArgs(int64(1)).WillReturnRows(batchRows(now, 2, 4))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT part_id, consumed_qty FROM batch_consumptions WHERE batch_id=? ORDER BY part_id")).
		WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"part_id", "consumed_qty"}).AddRow(20, 0.2))
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 0.1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1, updated_at=NOW() WHERE id=?")).
		WithArgs(float64(0.3), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectRestoreDraws(t, mock, 5, 0.2)
	expectPartAudit(t, mock, oldPart, "STOCK_ADJUST", map[string]any{
		"old_stock": float64(0.1),
		"new_stock": float64(0.3),
		"diff":      float64(0.2),
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

	if err := svc.RevokeBatch(usecase.RevokeBatchInput{ID: 1, Operator: "operator"}); err != nil {
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

	if err := svc.RevokeBatch(usecase.RevokeBatchInput{ID: 1, Operator: "operator"}); err != nil {
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

	err := svc.RevokeBatch(usecase.RevokeBatchInput{ID: 1, Operator: "operator"})
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
	// 批次号带当天日期，与服务同进程生成，用同一时钟算期望值。
	wantBatchNo := fmt.Sprintf("RK-%s-0077", time.Now().Format("20060102"))

	mock.ExpectBegin()
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 3))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1, updated_at=NOW() WHERE id=?")).
		WithArgs(float64(5), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	// 入库批次：先插行（batch_no 置 NULL）再按 id 回填，随后审计里带上批次号。
	mock.ExpectExec(inboundInsert).
		WithArgs(int64(20), float64(2), float64(2), nil).WillReturnResult(sqlmock.NewResult(77, 1))
	mock.ExpectExec(inboundSetBatchNo).
		WithArgs(wantBatchNo, int64(77)).WillReturnResult(sqlmock.NewResult(1, 1))
	expectPartAudit(t, mock, oldPart, "STOCK_IN", map[string]any{
		"old_stock": float64(3),
		"in_qty":    float64(2),
		"new_stock": float64(5),
		"batch_no":  wantBatchNo,
	}, "operator")
	mock.ExpectCommit()

	if _, err := svc.StockIn(usecase.StockInInput{PartID: 20, Qty: 2, Operator: "operator"}); err != nil {
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

	wantNo := fmt.Sprintf("RK-%s-0088", time.Now().Format("20060102"))

	mock.ExpectBegin()
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 3))
	// 差额为正：补一笔「盘点盈余」批次，总量与批次剩余之和保持相等。
	mock.ExpectExec(inboundInsert).WithArgs(int64(20), float64(5), float64(5), stringPtr("盘点盈余")).
		WillReturnResult(sqlmock.NewResult(88, 1))
	mock.ExpectExec(inboundSetBatchNo).WithArgs(wantNo, int64(88)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1, updated_at=NOW() WHERE id=?")).
		WithArgs(float64(8), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectPartAudit(t, mock, oldPart, "STOCK_ADJUST", map[string]any{
		"old_stock": float64(3),
		"new_stock": float64(8),
		"diff":      float64(5),
		"mode":      "总量盘点",
	}, "operator")
	mock.ExpectCommit()

	if err := svc.AdjustStock(usecase.AdjustStockInput{PartID: 20, NewQty: 8, Operator: "operator"}); err != nil {
		t.Fatalf("adjust stock: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

func TestAdjustStockQuantizesAuditDiff(t *testing.T) {
	svc, mock := newMockService(t)
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	oldPart := &model.Part{ID: 20, Code: "P-20", Name: "Part 20", Unit: "个", StockQty: 0.3, WarnQty: 0, Status: 1, Version: 1, CreatedAt: now, UpdatedAt: now, Operator: stringPtr("old")}

	mock.ExpectBegin()
	mock.ExpectQuery(partSelectForUpdate).WithArgs(int64(20)).WillReturnRows(partRows(now, 0.3))
	// 差额为负：先进先出扣批次剩余，但不记生产扣料明细（盘亏不归属任何批次）。
	mock.ExpectQuery(inboundSelectForUpdate).WithArgs(int64(20)).
		WillReturnRows(inboundRows(now, 5, 0.3, 0.3))
	mock.ExpectExec(inboundUpdateRemain).WithArgs(0.2, int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE parts SET stock_qty=?, version=version+1, updated_at=NOW() WHERE id=?")).
		WithArgs(0.2, int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	expectPartAudit(t, mock, oldPart, "STOCK_ADJUST", map[string]any{
		"old_stock": 0.3,
		"new_stock": 0.2,
		"diff":      -0.1,
		"mode":      "总量盘点",
	}, "operator")
	mock.ExpectCommit()

	if err := svc.AdjustStock(usecase.AdjustStockInput{PartID: 20, NewQty: 0.2, Operator: "operator"}); err != nil {
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
	// 标记跳过后清掉该零件在这批的投料记录与分配（先记投料后跳过的情况）。
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM batch_trace_allocs WHERE batch_id = ? AND part_id = ?")).
		WithArgs(int64(1), int64(20)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM batch_trace WHERE batch_id = ? AND part_id = ?")).
		WithArgs(int64(1), int64(20)).WillReturnResult(sqlmock.NewResult(0, 0))
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
