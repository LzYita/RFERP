package service

import (
	"strings"
	"testing"

	"app/internal/model"
	"app/internal/repository"
	"app/internal/usecase"
)

// 完成生产必须先记录投料：缺记录直接拒绝，并点名缺哪个零件。
func TestCompleteBatchRequiresRecordedTrace(t *testing.T) {
	f := newTraceAllocFixture(t)

	err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID: f.batchID, Status: batchStatusCompleted, Operator: "tester",
	})
	if err == nil || !strings.Contains(err.Error(), "必须记录投料") {
		t.Fatalf("未记录投料时完成应被拒：%v", err)
	}
	if !strings.Contains(err.Error(), "M-ALC") {
		t.Fatalf("拒绝文案没有点名零件：%v", err)
	}

	recordBatchTracesForTest(t, f.svc, f.batchID)
	if err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID: f.batchID, Status: batchStatusCompleted, Operator: "tester",
	}); err != nil {
		t.Fatalf("记录投料后完成应当成功：%v", err)
	}
}

// 临时零件只接受 BOM 表以外的零件。
func TestAddTempPartRejectsBOMPart(t *testing.T) {
	f := newTraceAllocFixture(t)
	err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: 1})
	if err == nil || !strings.Contains(err.Error(), "不能作为临时零件") {
		t.Fatalf("BOM 内零件不应能加成临时零件：%v", err)
	}
}

// 已完成/已撤销的批次不可记录投料。
func TestRecordTracesRejectsClosedBatch(t *testing.T) {
	f := newTraceAllocFixture(t)
	recordBatchTracesForTest(t, f.svc, f.batchID)
	if err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID: f.batchID, Status: batchStatusCompleted, Operator: "tester",
	}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	err := f.svc.RecordTraces([]*model.BatchTrace{{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 1,
	}})
	if err == nil || !strings.Contains(err.Error(), "不能记录投料") {
		t.Fatalf("已完成批次不应允许记录投料：%v", err)
	}
}

// 服务端兜底：分配数量不能超过该批可用（剩余 + 本批已分配 − 其它在产批次已分配）。
func TestRecordTracesRejectsOverAllocation(t *testing.T) {
	f := newTraceAllocFixtureWithStock(t, 10, 10) // 两笔各 10
	first := f.inbounds[0]

	err := f.svc.RecordTraces([]*model.BatchTrace{{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 15,
		Allocations: []model.BatchTraceAlloc{{InboundID: first.ID, Qty: 15}},
	}})
	if err == nil || !strings.Contains(err.Error(), "可用量只有") {
		t.Fatalf("超过批次可用量应被拒绝：%v", err)
	}
}

// 标记跳过时写两条审计：跳过本身 + 被清掉的投料记录。
func TestSkipPartWritesAudit(t *testing.T) {
	f := newTraceAllocFixture(t)
	first := f.inbounds[0]
	if err := f.svc.RecordTraces([]*model.BatchTrace{{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 10,
		Allocations: []model.BatchTraceAlloc{{InboundID: first.ID, Qty: 10}},
	}}); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := f.svc.AddSkipPart(f.batchID, f.partID, "tester"); err != nil {
		t.Fatalf("skip: %v", err)
	}

	for _, table := range []string{"batch_skip_parts", "batch_trace"} {
		logs, err := f.store.ListAuditLogs(table, f.partID)
		if err != nil {
			t.Fatalf("audit %s: %v", table, err)
		}
		if len(logs) == 0 {
			t.Fatalf("%s 没有写审计", table)
		}
		if logs[0].Operator == nil || *logs[0].Operator != "tester" {
			t.Fatalf("%s 审计缺少操作人：%+v", table, logs[0].Operator)
		}
	}
}

// 移除临时零件后，它的投料记录与批次占用一起消失。
func TestRemoveTempPartClearsTraceAndOccupancy(t *testing.T) {
	f := newTraceAllocFixtureWithStock(t, 10, 10)
	// 造一个 BOM 外的零件与一批临时用量。
	extra := int64(0)
	err := f.store.WithTx(func(tx repository.TxOps) error {
		pid, err := tx.CreatePart(&model.Part{Code: "M-EXTRA", Name: "临时件", Unit: "个", Status: 1})
		if err != nil {
			return err
		}
		extra = pid
		return tx.AddTempPart(&model.BatchTempPart{BatchID: f.batchID, PartID: pid, Qty: 4})
	})
	if err != nil {
		t.Fatalf("seed temp: %v", err)
	}
	stockPart(t, f.svc, extra, 10)
	first := f.inbounds[0]
	other, err := f.svc.ListPartInbounds(extra)
	if err != nil || len(other) == 0 {
		t.Fatalf("extra inbounds: %v", err)
	}
	if err := f.svc.RecordTraces([]*model.BatchTrace{{
		BatchID: f.batchID, PartID: extra, UsedQty: 4,
		Allocations: []model.BatchTraceAlloc{{InboundID: other[0].ID, Qty: 4}},
	}}); err != nil {
		t.Fatalf("record extra: %v", err)
	}
	// 另一个零件的记录保持不动作对照。
	if err := f.svc.RecordTraces([]*model.BatchTrace{{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 10,
		Allocations: []model.BatchTraceAlloc{{InboundID: first.ID, Qty: 10}},
	}}); err != nil {
		t.Fatalf("record: %v", err)
	}

	if err := f.svc.RemoveTempPart(f.batchID, extra, "tester"); err != nil {
		t.Fatalf("remove temp: %v", err)
	}
	rows, err := f.svc.GetTraceByBatch(f.batchID)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	for _, r := range rows {
		if r.PartID == extra {
			t.Fatalf("移除临时零件后投料记录仍在：%+v", r)
		}
	}
	inbounds, err := f.svc.ListPartInbounds(extra)
	if err != nil {
		t.Fatalf("list inbounds: %v", err)
	}
	for _, in := range inbounds {
		if in.Occupied != 0 {
			t.Fatalf("移除临时零件后仍占用 %v：%+v", in.Occupied, in)
		}
	}
}

// 旧的单条接口也走同一套规则：覆盖而不是追加，供应商取零件台账。
func TestRecordTraceSingleUsesReplaceSemantics(t *testing.T) {
	f := newTraceAllocFixture(t)
	first := f.inbounds[0]
	if _, err := f.svc.RecordTrace(&model.BatchTrace{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 10,
		Allocations: []model.BatchTraceAlloc{{InboundID: first.ID, Qty: 10}},
	}); err != nil {
		t.Fatalf("first record: %v", err)
	}
	if _, err := f.svc.RecordTrace(&model.BatchTrace{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 4,
	}); err != nil {
		t.Fatalf("second record: %v", err)
	}
	rows, err := f.svc.GetTraceByBatch(f.batchID)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if len(rows) != 1 || rows[0].UsedQty != 4 {
		t.Fatalf("单条接口应当是覆盖语义：%+v", rows)
	}
	if rows[0].Supplier == nil || *rows[0].Supplier != sampleSupplier {
		t.Fatalf("供应商没有取零件台账：%+v", rows[0].Supplier)
	}
}
