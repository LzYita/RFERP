package service

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"app/internal/model"
)

// v18 起记录投料是「按零件覆盖」：先清分配与旧记录，再写新记录与分配。
// 一条失败整批回滚——留下半套记录会让追溯表和实际投料对不上。
func TestRecordTracesReplacesPerPartAndRollsBack(t *testing.T) {
	svc, mock := newMockService(t)
	operator := "operator"
	batchNo := "PB-1"
	supplier := "Supplier"
	traces := []*model.BatchTrace{
		{BatchID: 1, PartID: 20, PartBatchNo: &batchNo, UsedQty: 1, Supplier: &supplier, Operator: &operator},
		{BatchID: 1, PartID: 21, PartBatchNo: &batchNo, UsedQty: 2, Supplier: &supplier, Operator: &operator},
	}
	allocClear := regexp.QuoteMeta("DELETE FROM batch_trace_allocs WHERE batch_id = ? AND part_id = ?")
	traceClear := regexp.QuoteMeta("DELETE FROM batch_trace WHERE batch_id = ? AND part_id = ?")
	traceInsert := regexp.QuoteMeta("INSERT INTO batch_trace (batch_id,part_id,part_batch_no,used_qty,supplier,operator) VALUES (?,?,?,?,?,?)")

	mock.ExpectBegin()
	mock.ExpectExec(allocClear).WithArgs(int64(1), int64(20)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(traceClear).WithArgs(int64(1), int64(20)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(traceInsert).
		WithArgs(int64(1), int64(20), &batchNo, float64(1), &supplier, &operator).
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectExec(allocClear).WithArgs(int64(1), int64(21)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(traceClear).WithArgs(int64(1), int64(21)).WillReturnResult(sqlmock.NewResult(0, 1))
	insertErr := errors.New("second trace failed")
	mock.ExpectExec(traceInsert).
		WithArgs(int64(1), int64(21), &batchNo, float64(2), &supplier, &operator).
		WillReturnError(insertErr)
	mock.ExpectRollback()

	if err := svc.RecordTraces(traces); !errors.Is(err, insertErr) {
		t.Fatalf("RecordTraces returned the wrong error: %v", err)
	}
	if traces[0].ID != 7 {
		t.Fatalf("first trace id = %d, want 7", traces[0].ID)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}

// 分配合计超过用量是表单算错，必须在写库前拒绝。
func TestRecordTracesRejectsAllocTotalAboveUsage(t *testing.T) {
	svc, _ := newMockService(t)
	traces := []*model.BatchTrace{{
		BatchID: 1, PartID: 20, UsedQty: 10,
		Allocations: []model.BatchTraceAlloc{
			{InboundID: 5, Qty: 8},
			{InboundID: 6, Qty: 4},
		},
	}}
	err := svc.RecordTraces(traces)
	if err == nil {
		t.Fatal("分配合计 12 超过用量 10，应被拒绝")
	}
	if !regexp.MustCompile("超过了用量").MatchString(err.Error()) {
		t.Fatalf("错误文案没有说明超量: %v", err)
	}
}

// 同一批次同一零件出现两条记录：以前会追加成两行，现在必须拒绝。
func TestRecordTracesRejectsDuplicateBatchPart(t *testing.T) {
	svc, _ := newMockService(t)
	traces := []*model.BatchTrace{
		{BatchID: 1, PartID: 20, UsedQty: 1},
		{BatchID: 1, PartID: 20, UsedQty: 2},
	}
	err := svc.RecordTraces(traces)
	if err == nil {
		t.Fatal("重复的 (batch, part) 应被拒绝")
	}
	if !regexp.MustCompile("重复").MatchString(err.Error()) {
		t.Fatalf("错误文案没有说明重复: %v", err)
	}
}
