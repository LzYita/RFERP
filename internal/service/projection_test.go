package service

import (
	"path/filepath"
	"strings"
	"testing"

	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"
	"app/internal/usecase"
)

// 投影是"把在产批次将要消耗的量先扣掉"，所以它必须与完成生产时的扣料
// 逐位一致。这个文件用真实 SQLite 跑完整装配，而不是打桩：
// 桩能验证公式，验证不了"投影说够、完成却被拒"这类跨层漂移。
func newProjectionFixture(t *testing.T) (*Service, repository.Store) {
	t.Helper()
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "proj.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := repository.NewSQLite(db)
	return NewWithSnapshot(store, nil, nil), store
}

type projFixture struct {
	svc      *Service
	store    repository.Store
	partID   int64
	product  int64
	batchIDs map[string]int64
}

// seedProjectionFixture 建一个产品 + 一个零件 + 一条 BOM（每台 1 个、无损耗），
// 再按 batchNos 建若干待生产批次。
func seedProjectionFixture(t *testing.T, stock, warn float64, planQty int, batchNos ...string) *projFixture {
	t.Helper()
	svc, store := newProjectionFixture(t)
	f := &projFixture{svc: svc, store: store, batchIDs: map[string]int64{}}

	err := store.WithTx(func(tx repository.TxOps) error {
		pid, err := tx.CreateProduct(&model.Product{Code: "P-PROJ", Name: "投影验证产品", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		f.product = pid
		partID, err := tx.CreatePart(&model.Part{
			Code: "M-PROJ", Name: "投影验证零件", Unit: "个",
			StockQty: stock, WarnQty: warn, Status: 1,
		})
		if err != nil {
			return err
		}
		f.partID = partID
		if _, err := tx.CreateBOMItem(&model.BOMItem{
			ProductID: pid, PartID: partID, Quantity: 1, LossRate: 0, UseMode: 0,
		}); err != nil {
			return err
		}
		for _, no := range batchNos {
			id, err := tx.CreateBatch(&model.ProductBatch{
				BatchNo: no, ProductID: pid, PlanQty: planQty, Status: batchStatusPending,
			})
			if err != nil {
				return err
			}
			f.batchIDs[no] = id
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return f
}

func (f *projFixture) projection(t *testing.T, in usecase.StockProjectionInput) usecase.PartProjection {
	t.Helper()
	res, err := f.svc.StockProjection(in)
	if err != nil {
		t.Fatalf("StockProjection: %v", err)
	}
	for _, p := range res.Parts {
		if p.PartID == f.partID {
			return p
		}
	}
	return usecase.PartProjection{PartID: f.partID}
}

// 两个待生产批次各需 60、库存 100：单独看每批都"够"，合起来缺 20。
// 这正是"未点完成时零件仍显示库存充足"的场景。
func TestStockProjectionAggregatesOpenBatches(t *testing.T) {
	f := seedProjectionFixture(t, 100, 20, 60, "B-1", "B-2")

	got := f.projection(t, usecase.StockProjectionInput{})
	if got.Committed != 120 {
		t.Fatalf("committed = %v，期望 120", got.Committed)
	}
	if got.After != -20 {
		t.Fatalf("after = %v，期望 -20", got.After)
	}
	if len(got.Holders) != 2 {
		t.Fatalf("holders = %+v，期望两个批次", got.Holders)
	}
	// 来源必须带 batch_id：工作台要从"零件"跳到"哪些批次在用"。
	for _, h := range got.Holders {
		if h.BatchID == 0 || h.BatchNo == "" {
			t.Fatalf("holder 缺少批次标识：%+v", h)
		}
	}

	res, err := f.svc.StockProjection(usecase.StockProjectionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.WillShort != 1 || res.Summary.WillWarn != 0 {
		t.Fatalf("summary = %+v，期望 WillShort=1 WillWarn=0", res.Summary)
	}
	// 两个批次共用同一个缺料零件，所以两批的备料结论都是"做不出来"。
	if len(res.Batches) != 2 {
		t.Fatalf("batches = %+v，期望两个在产批次", res.Batches)
	}
	for _, b := range res.Batches {
		if b.Level != "short" || len(b.Parts) != 1 || b.Parts[0].PartID != f.partID {
			t.Fatalf("批次备料结论 = %+v，期望 short 且点名该零件", b)
		}
	}
}

// 只"将低于预警线"的批次结论是 low，不能报成做不出来。
func TestStockProjectionBatchLevelLow(t *testing.T) {
	// 库存 100、预警线 90、一批 20 → 完成后 80，低于预警线但仍为正。
	f := seedProjectionFixture(t, 100, 90, 20, "B-LOW")
	res, err := f.svc.StockProjection(usecase.StockProjectionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Batches) != 1 || res.Batches[0].Level != "low" {
		t.Fatalf("batches = %+v，期望 low", res.Batches)
	}
	if res.Summary.WillShort != 0 || res.Summary.WillWarn != 1 {
		t.Fatalf("summary = %+v，期望 WillWarn=1", res.Summary)
	}
}

// 建批预览：把"还没创建的这一批"也算进去，并标成计划中。
func TestStockProjectionIncludesPlannedBatch(t *testing.T) {
	f := seedProjectionFixture(t, 100, 20, 60, "B-1")

	got := f.projection(t, usecase.StockProjectionInput{ProductID: f.product, PlanQty: 10})
	if got.Committed != 70 {
		t.Fatalf("committed = %v，期望 70（60 在产 + 10 计划）", got.Committed)
	}
	if len(got.Holders) != 2 || !got.Holders[0].Planned {
		t.Fatalf("holders = %+v，期望计划中的一批排在最前", got.Holders)
	}
}

// 只统计启用中的零件：停用件不会补货，不该反复出现在警告里。
func TestStockProjectionIgnoresDisabledPartsInSummary(t *testing.T) {
	f := seedProjectionFixture(t, 100, 20, 60, "B-1", "B-2")
	if err := f.store.WithTx(func(tx repository.TxOps) error {
		p, err := tx.GetPartForUpdate(f.partID)
		if err != nil {
			return err
		}
		p.Status = 2
		_, err = tx.UpdatePart(p)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.StockProjection(usecase.StockProjectionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Summary.WillShort != 0 || len(res.Parts) != 1 {
		t.Fatalf("summary = %+v parts=%d：零件行仍要在，但计数不能算停用件", res.Summary, len(res.Parts))
	}
}

// 不变量：投影说"缺料" ⇔ 现在点完成会被拒。
//
// 这条比任何单个数字都重要——两边一旦分叉，界面上的预警就是在骗人。
func TestProjectionShortageMatchesCompletionRejection(t *testing.T) {
	cases := []struct {
		name  string
		stock float64
		need  int
	}{
		{"够", 100, 60},
		{"恰好相等", 60, 60},
		{"差一点", 59, 60},
		{"零库存", 0, 60},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := seedProjectionFixture(t, c.stock, 0, c.need, "B-ONLY")
			proj := f.projection(t, usecase.StockProjectionInput{})
			wantShort := proj.After < 0

			err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
				ID: f.batchIDs["B-ONLY"], Status: 2, Operator: "测试",
			})
			gotRejected := err != nil
			if wantShort != gotRejected {
				t.Fatalf("投影 after=%v（缺料=%v），完成结果 err=%v：两者必须一致",
					proj.After, wantShort, err)
			}
			if gotRejected && !strings.Contains(err.Error(), "库存不足") {
				t.Fatalf("拒绝原因不是库存不足：%v", err)
			}
		})
	}
}

// 完成被拒时要说清"库存还被哪些在产批次占着"，否则用户只补本批的差额。
func TestCompletionShortageNamesOtherBatches(t *testing.T) {
	f := seedProjectionFixture(t, 100, 0, 60, "B-1", "B-2", "B-3")
	// 先完成 B-1：60 ≤ 100，成功，剩 40。
	if err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID: f.batchIDs["B-1"], Status: 2, Operator: "测试",
	}); err != nil {
		t.Fatalf("B-1 应当完成成功：%v", err)
	}
	// 再完成 B-2：需要 60、剩 40，被拒；此时 B-3 仍在产并占用 60。
	err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID: f.batchIDs["B-2"], Status: 2, Operator: "测试",
	})
	if err == nil {
		t.Fatal("B-2 应当因库存不足被拒")
	}
	msg := err.Error()
	if !strings.Contains(msg, "另有在产批次占用") || !strings.Contains(msg, "B-3") {
		t.Fatalf("文案没有说明占用来源：%s", msg)
	}
}

// use_mode=1（每 M 台用 1 个）的向上取整必须与扣料一致；跳过零件不参与占用。
func TestStockProjectionHonoursUseModeAndSkips(t *testing.T) {
	svc, store := newProjectionFixture(t)
	var partID int64
	if err := store.WithTx(func(tx repository.TxOps) error {
		pid, err := tx.CreateProduct(&model.Product{Code: "P-M", Name: "包装产品", Unit: "台", Status: 1})
		if err != nil {
			return err
		}

		q, err := tx.CreatePart(&model.Part{Code: "M-BOX", Name: "外箱", Unit: "个", StockQty: 100, WarnQty: 0, Status: 1})
		if err != nil {
			return err
		}
		partID = q
		// 每 4 台用 1 个外箱、损耗 10%
		if _, err := tx.CreateBOMItem(&model.BOMItem{
			ProductID: pid, PartID: q, Quantity: 4, LossRate: 10, UseMode: 1,
		}); err != nil {
			return err
		}
		// 计划 25 台 → ceil(25/4)=7 → ×1.1=7.7 → ceil=8
		if _, err := tx.CreateBatch(&model.ProductBatch{
			BatchNo: "B-BOX", ProductID: pid, PlanQty: 25, Status: batchStatusPending,
		}); err != nil {
			return err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.StockProjection(usecase.StockProjectionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Parts) != 1 || res.Parts[0].PartID != partID {
		t.Fatalf("parts = %+v", res.Parts)
	}
	if res.Parts[0].Committed != 8 {
		t.Fatalf("committed = %v，期望 8（ceil(25/4)=7，含 10%% 损耗后向上取整）", res.Parts[0].Committed)
	}
}
