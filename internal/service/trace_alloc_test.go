package service

import (
	"path/filepath"
	"testing"

	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"
	"app/internal/usecase"
)

// 投料的批次分配（v18）：一个零件一条投料记录，挂若干条「先扣哪批」，
// 完成生产按这个顺序扣，撤销按原批次还回。用真实 SQLite 钉住整条链路。
type traceAllocFixture struct {
	svc     *Service
	store   repository.Store
	partID  int64
	batchID int64
	// inbounds 按入库先后：inbounds[0] 是先入的（id 小）
	inbounds []model.PartInbound
}

func newTraceAllocFixture(t *testing.T) *traceAllocFixture {
	return newTraceAllocFixtureWithStock(t, 10, 10)
}

// newTraceAllocFixtureWithStock 用两笔入库建立库存：先入 firstQty，再入 secondQty。
func newTraceAllocFixtureWithStock(t *testing.T, firstQty, secondQty float64) *traceAllocFixture {
	t.Helper()
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "trace.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := repository.NewSQLite(db)
	svc := NewWithSnapshot(store, nil, nil)
	f := &traceAllocFixture{svc: svc, store: store}

	err = store.WithTx(func(tx repository.TxOps) error {
		pid, err := tx.CreateProduct(&model.Product{Code: "P-ALC", Name: "分配验证产品", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		partID, err := tx.CreatePart(&model.Part{Code: "M-ALC", Name: "分配验证零件", Unit: "个", Status: 1})
		if err != nil {
			return err
		}
		f.partID = partID
		if _, err := tx.CreateBOMItem(&model.BOMItem{ProductID: pid, PartID: partID, Quantity: 1, LossRate: 0}); err != nil {
			return err
		}
		batchID, err := tx.CreateBatch(&model.ProductBatch{
			BatchNo: "B-ALC", ProductID: pid, PlanQty: 10, Status: batchStatusPending,
		})
		if err != nil {
			return err
		}
		f.batchID = batchID
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	stockPart(t, svc, f.partID, firstQty)
	stockPart(t, svc, f.partID, secondQty)
	list, err := svc.ListPartInbounds(f.partID)
	if err != nil || len(list) != 2 {
		t.Fatalf("inbounds = %d, err = %v; want 2", len(list), err)
	}
	// ListPartInbounds 按时间倒序（最新在前），这里翻成入库先后。
	f.inbounds = []model.PartInbound{list[1], list[0]}
	return f
}

func (f *traceAllocFixture) remainingByBatch(t *testing.T) map[string]float64 {
	t.Helper()
	rows, err := f.svc.ListPartInbounds(f.partID)
	if err != nil {
		t.Fatalf("list inbounds: %v", err)
	}
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		out[r.BatchNo] = r.Remaining
	}
	return out
}

// 记录投料：一条记录 + 两条分配；再记录一次是覆盖，不是追加。
func TestRecordTracesReplacesPerPart(t *testing.T) {
	f := newTraceAllocFixture(t)
	first, second := f.inbounds[0], f.inbounds[1]

	err := f.svc.RecordTraces([]*model.BatchTrace{{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 10,
		Allocations: []model.BatchTraceAlloc{
			{InboundID: second.ID, Qty: 6},
			{InboundID: first.ID, Qty: 4},
		},
	}})
	if err != nil {
		t.Fatalf("record traces: %v", err)
	}
	rows, err := f.svc.GetTraceByBatch(f.batchID)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("trace rows = %d, want 1", len(rows))
	}
	if len(rows[0].Allocations) != 2 {
		t.Fatalf("allocations = %d, want 2", len(rows[0].Allocations))
	}
	if rows[0].Allocations[0].InboundID != second.ID || rows[0].Allocations[1].InboundID != first.ID {
		t.Fatalf("分配顺序没有保留：%+v", rows[0].Allocations)
	}

	// 改用量再记录：仍然只有一条，分配按新的覆盖。
	err = f.svc.RecordTraces([]*model.BatchTrace{{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 5,
		Allocations: []model.BatchTraceAlloc{{InboundID: first.ID, Qty: 5}},
	}})
	if err != nil {
		t.Fatalf("record traces again: %v", err)
	}
	rows, err = f.svc.GetTraceByBatch(f.batchID)
	if err != nil {
		t.Fatalf("get trace: %v", err)
	}
	if len(rows) != 1 || rows[0].UsedQty != 5 {
		t.Fatalf("覆盖后 rows = %+v, want 1 行 used=5", rows)
	}
	if len(rows[0].Allocations) != 1 || rows[0].Allocations[0].InboundID != first.ID {
		t.Fatalf("覆盖后分配 = %+v", rows[0].Allocations)
	}
}

// 完成生产按分配顺序扣：先扣第二笔入库 6，再扣第一笔 4。
func TestCompleteBatchDrawsInAllocatedOrder(t *testing.T) {
	f := newTraceAllocFixture(t)
	first, second := f.inbounds[0], f.inbounds[1]

	if err := f.svc.RecordTraces([]*model.BatchTrace{{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 10,
		Allocations: []model.BatchTraceAlloc{
			{InboundID: second.ID, Qty: 6},
			{InboundID: first.ID, Qty: 4},
		},
	}}); err != nil {
		t.Fatalf("record traces: %v", err)
	}
	// 记录完还没完成：占用可见，剩余未动。
	occ, err := f.svc.ListPartInbounds(f.partID)
	if err != nil {
		t.Fatalf("list inbounds: %v", err)
	}
	occByBatch := make(map[string]float64, len(occ))
	for _, r := range occ {
		occByBatch[r.BatchNo] = r.Occupied
	}
	if occByBatch[first.BatchNo] != 4 || occByBatch[second.BatchNo] != 6 {
		t.Fatalf("占用 = %v, want 第一笔 4 / 第二笔 6", occByBatch)
	}

	if err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID: f.batchID, Status: batchStatusCompleted, Operator: "tester",
	}); err != nil {
		t.Fatalf("complete batch: %v", err)
	}
	if got := partStock(t, f.svc, f.partID); got != 10 {
		t.Fatalf("stock = %v, want 10", got)
	}
	rem := f.remainingByBatch(t)
	if rem[second.BatchNo] != 4 || rem[first.BatchNo] != 6 {
		t.Fatalf("完成后的批次剩余 = %v, want 第二笔 4 / 第一笔 6", rem)
	}
	// 完成后的占用清零（已完成批次的消耗已经体现在剩余里）。
	after, err := f.svc.ListPartInbounds(f.partID)
	if err != nil {
		t.Fatalf("list inbounds: %v", err)
	}
	for _, r := range after {
		if r.Occupied != 0 {
			t.Fatalf("完成后批次 %s 仍有占用 %v", r.BatchNo, r.Occupied)
		}
	}

	// 撤销按原批次还回，顺序相反也一样还回原位。
	if err := f.svc.RevokeBatch(usecase.RevokeBatchInput{ID: f.batchID, Operator: "tester"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	rem = f.remainingByBatch(t)
	if rem[second.BatchNo] != 10 || rem[first.BatchNo] != 10 {
		t.Fatalf("撤销后的批次剩余 = %v, want 两批各 10", rem)
	}
}

// 排在前面的批次不够时，余量按先进先出兜底：第二笔只有 3，还差 7 → 扣第一笔 7。
//
// 扣料按「排在前面的先扣到用完」执行，计划里的数量只决定展示与校验；
// 库存在计划之后变了（这里第二笔只有 3），顺序仍然生效。
func TestCompleteBatchFallsBackToFIFOWhenOrderedBatchRunsOut(t *testing.T) {
	f := newTraceAllocFixtureWithStock(t, 10, 3)
	first, second := f.inbounds[0], f.inbounds[1]

	if err := f.svc.RecordTraces([]*model.BatchTrace{{
		BatchID: f.batchID, PartID: f.partID, UsedQty: 10,
		Allocations: []model.BatchTraceAlloc{{InboundID: second.ID, Qty: 3}},
	}}); err != nil {
		t.Fatalf("record traces: %v", err)
	}
	if err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID: f.batchID, Status: batchStatusCompleted, Operator: "tester",
	}); err != nil {
		t.Fatalf("complete batch: %v", err)
	}
	rem := f.remainingByBatch(t)
	if rem[second.BatchNo] != 0 || rem[first.BatchNo] != 3 {
		t.Fatalf("批次剩余 = %v, want 第二笔 0 / 第一笔 3（余量走先进先出）", rem)
	}
}
