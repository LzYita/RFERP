package service

import (
	"testing"

	"app/internal/model"
)

// recordBatchTracesForTest 给一个批次把「该扣的零件」都记上投料（不带批次分配，
// 完成时走先进先出）。完成生产现在要求先记录投料，真实 SQLite 用例统一用它铺前置条件。
func recordBatchTracesForTest(t *testing.T, svc *Service, batchID int64) {
	t.Helper()
	batches, err := svc.ListBatches()
	if err != nil {
		t.Fatalf("list batches: %v", err)
	}
	var batch *model.ProductBatch
	for i := range batches {
		if batches[i].ID == batchID {
			batch = &batches[i]
			break
		}
	}
	if batch == nil {
		t.Fatalf("batch %d not found", batchID)
	}
	bom, err := svc.GetBOMByProduct(batch.ProductID)
	if err != nil {
		t.Fatalf("bom: %v", err)
	}
	skipped, err := svc.GetSkippedParts(batchID)
	if err != nil {
		t.Fatalf("skipped: %v", err)
	}
	skip := make(map[int64]bool, len(skipped))
	for _, id := range skipped {
		skip[id] = true
	}
	usage := make(map[int64]float64, len(bom))
	for _, item := range bom {
		if skip[item.PartID] {
			continue
		}
		q := quantizeQuantity(bomConsume(batch.PlanQty, item))
		if q <= 0 {
			continue
		}
		usage[item.PartID] = quantizeQuantity(usage[item.PartID] + q)
	}
	temps, err := svc.ListTempParts(batchID)
	if err != nil {
		t.Fatalf("temp parts: %v", err)
	}
	for _, tp := range temps {
		q := quantizeQuantity(tp.Qty)
		if q <= 0 {
			continue
		}
		usage[tp.PartID] = quantizeQuantity(usage[tp.PartID] + q)
	}
	rows := make([]*model.BatchTrace, 0, len(usage))
	for partID, q := range usage {
		rows = append(rows, &model.BatchTrace{BatchID: batchID, PartID: partID, UsedQty: q})
	}
	if len(rows) == 0 {
		return
	}
	if err := svc.RecordTraces(rows); err != nil {
		t.Fatalf("record traces for batch %d: %v", batchID, err)
	}
}
