package service

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"app/internal/model"
)

func TestRecordTracesRollsBackAllRowsWhenOneInsertFails(t *testing.T) {
	svc, mock := newMockService(t)
	operator := "operator"
	batchNo := "PB-1"
	supplier := "Supplier"
	traces := []*model.BatchTrace{
		{BatchID: 1, PartID: 20, PartBatchNo: &batchNo, UsedQty: 1, Supplier: &supplier, Operator: &operator},
		{BatchID: 1, PartID: 21, PartBatchNo: &batchNo, UsedQty: 2, Supplier: &supplier, Operator: &operator},
	}
	traceInsert := regexp.QuoteMeta("INSERT INTO batch_trace (batch_id,part_id,part_batch_no,used_qty,supplier,operator) VALUES (?,?,?,?,?,?)")

	mock.ExpectBegin()
	mock.ExpectExec(traceInsert).
		WithArgs(int64(1), int64(20), &batchNo, float64(1), &supplier, &operator).
		WillReturnResult(sqlmock.NewResult(1, 1))
	insertErr := errors.New("second trace failed")
	mock.ExpectExec(traceInsert).
		WithArgs(int64(1), int64(21), &batchNo, float64(2), &supplier, &operator).
		WillReturnError(insertErr)
	mock.ExpectRollback()

	if err := svc.RecordTraces(traces); !errors.Is(err, insertErr) {
		t.Fatalf("RecordTraces returned the wrong error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("database expectations: %v", err)
	}
}
