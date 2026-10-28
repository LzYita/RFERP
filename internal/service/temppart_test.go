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

// 临时零件是"这批实际要用、但不在 BOM 里"的料：加的时候要查库存，
// 完成时要扣、投影要算占用、撤销要回退。这里用真实 SQLite 把四件事都钉住。
func newTempPartFixture(t *testing.T) (*Service, repository.Store) {
	t.Helper()
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "temp.db"))
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

type tempFixture struct {
	svc     *Service
	store   repository.Store
	partID  int64
	otherID int64
	batchID int64
}

// 一个产品（无 BOM）+ 两个零件 + 一个待生产批次。
func seedTempFixture(t *testing.T, stock, otherStock float64) *tempFixture {
	t.Helper()
	svc, store := newTempPartFixture(t)
	f := &tempFixture{svc: svc, store: store}
	err := store.WithTx(func(tx repository.TxOps) error {
		pid, err := tx.CreateProduct(&model.Product{Code: "P-TEMP", Name: "临时零件验证产品", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		partID, err := tx.CreatePart(&model.Part{
			Code: "M-TEMP", Name: "临时验证零件", Unit: "个", WarnQty: 0, Status: 1,
		})
		if err != nil {
			return err
		}
		f.partID = partID
		otherID, err := tx.CreatePart(&model.Part{
			Code: "M-OTHER", Name: "对照零件", Unit: "个", WarnQty: 0, Status: 1,
		})
		if err != nil {
			return err
		}
		f.otherID = otherID
		batchID, err := tx.CreateBatch(&model.ProductBatch{
			BatchNo: "B-TEMP", ProductID: pid, PlanQty: 10, Status: batchStatusPending,
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
	// 库存经入库建立：每个零件一笔批次，扣料/撤销才有批次可扣可还。
	stockPart(t, svc, f.partID, stock)
	stockPart(t, svc, f.otherID, otherStock)
	return f
}

// 库存够 → 添加成功；投影把它算成占用。
func TestAddTempPartProjectsIntoCommitted(t *testing.T) {
	f := seedTempFixture(t, 50, 100)
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: 30}); err != nil {
		t.Fatalf("AddTempPart: %v", err)
	}

	rows, err := f.svc.ListTempParts(f.batchID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListTempParts = %+v err=%v，期望一行", rows, err)
	}
	if rows[0].Qty != 30 || rows[0].Code != "M-TEMP" {
		t.Fatalf("row = %+v", rows[0])
	}

	res, err := f.svc.StockProjection(usecase.StockProjectionInput{})
	if err != nil {
		t.Fatal(err)
	}
	var proj *usecase.PartProjection
	for i := range res.Parts {
		if res.Parts[i].PartID == f.partID {
			proj = &res.Parts[i]
		}
	}
	if proj == nil || proj.Committed != 30 || proj.After != 20 {
		t.Fatalf("projection = %+v，期望 committed=30 after=20", proj)
	}
}

// 库存不够（含其它在产批次的占用）→ 拒绝，并点名占用来源。
//
// 检测口径与投影一致：stock - Σ(全部在产批次的 BOM+临时占用) - 新数量 < 0 拒。
// 两个批次各占 40、库存 50 时，任何加料都会越过"全部完成"的总账。
func TestAddTempPartRefusesWhenStockShort(t *testing.T) {
	// 库存 50，两个待生产批次（同产品、BOM 每台 1 个、各计划 40）合计占 80。
	svc, store := newTempPartFixture(t)
	var partID, batchA, batchB int64
	_ = batchB // 只用于占位：B-B 的存在让合计占用变成 80
	err := store.WithTx(func(tx repository.TxOps) error {
		pid, err := tx.CreateProduct(&model.Product{Code: "P-T2", Name: "两批产品", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		partID, err = tx.CreatePart(&model.Part{Code: "M-T2", Name: "共用零件", Unit: "个", StockQty: 50, WarnQty: 0, Status: 1})
		if err != nil {
			return err
		}
		if _, err := tx.CreateBOMItem(&model.BOMItem{ProductID: pid, PartID: partID, Quantity: 1}); err != nil {
			return err
		}
		batchA, err = tx.CreateBatch(&model.ProductBatch{BatchNo: "B-A", ProductID: pid, PlanQty: 40, Status: batchStatusPending})
		if err != nil {
			return err
		}
		batchB, err = tx.CreateBatch(&model.ProductBatch{BatchNo: "B-B", ProductID: pid, PlanQty: 40, Status: batchStatusPending})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	err = svc.AddTempPart(usecase.AddTempPartInput{BatchID: batchA, PartID: partID, Qty: 20})
	if err == nil {
		t.Fatal("占用叠加后应当拒绝")
	}
	msg := err.Error()
	if !strings.Contains(msg, "将缺 50") || !strings.Contains(msg, "B-A") || !strings.Contains(msg, "B-B") {
		t.Fatalf("拒绝文案没有讲清缺口与占用：%s", msg)
	}
}

// 单批次、无其它占用时的边界：恰好把库存用完允许，再超 1 个就拒。
func TestAddTempPartBoundaryExactStock(t *testing.T) {
	f := seedTempFixture(t, 50, 0)
	// 该产品没有 BOM（seedTempFixture 不建 BOM），占用只有临时零件本身。
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: 50}); err != nil {
		t.Fatalf("恰好用完应当允许：%v", err)
	}
	// 改成 51：旧数量 50 不算占用，51 > 50 拒。
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: 51}); err == nil {
		t.Fatal("超过库存应当拒绝")
	}
}

// 重复添加同一零件 = 改数量（upsert），不是再插一行。
func TestAddTempPartUpsertsQuantity(t *testing.T) {
	f := seedTempFixture(t, 100, 0)
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: 10}); err != nil {
		t.Fatal(err)
	}
	// 改成 8：检测时旧数量 10 不算占用（这次就是来改它的）。
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: 8}); err != nil {
		t.Fatalf("改数量应当成功：%v", err)
	}
	rows, err := f.svc.ListTempParts(f.batchID)
	if err != nil || len(rows) != 1 || rows[0].Qty != 8 {
		t.Fatalf("rows = %+v err=%v，期望一行且数量为 8", rows, err)
	}
}

// 完成生产：临时零件按数量整笔扣，缺料时拒绝，撤销时按冻结记录回退。
func TestTempPartCompletionDeductsAndRevokeRestores(t *testing.T) {
	f := seedTempFixture(t, 20, 10)
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: 15}); err != nil {
		t.Fatal(err)
	}
	// 对照零件也加临时 5，验证一次扣多个。
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.otherID, Qty: 5}); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID: f.batchID, Status: 2, Operator: "测试",
	}); err != nil {
		t.Fatalf("完成应当成功：%v", err)
	}
	p1, _ := f.store.GetPart(f.partID)
	p2, _ := f.store.GetPart(f.otherID)
	if p1.StockQty != 5 || p2.StockQty != 5 {
		t.Fatalf("库存 = %v/%v，期望 5/5", p1.StockQty, p2.StockQty)
	}

	// 撤销：按 batch_consumptions 冻结记录回退，临时零件与 BOM 同待遇。
	if err := f.svc.RevokeBatch(usecase.RevokeBatchInput{ID: f.batchID, Operator: "测试"}); err != nil {
		t.Fatalf("撤销应当成功：%v", err)
	}
	p1, _ = f.store.GetPart(f.partID)
	p2, _ = f.store.GetPart(f.otherID)
	if p1.StockQty != 20 || p2.StockQty != 10 {
		t.Fatalf("撤销后库存 = %v/%v，期望 20/10", p1.StockQty, p2.StockQty)
	}
}

// 完成时临时零件不够 → 整批拒绝（缺料即中止，与 BOM 同语义），
// 且文案能说出"本批其余用料扣完后剩多少"。
func TestTempPartShortageBlocksCompletion(t *testing.T) {
	f := seedTempFixture(t, 20, 0)
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: 15}); err != nil {
		t.Fatal(err)
	}
	// 添加后偷偷把库存调低到 10：模拟"加的时候够、完成的时候不够"。
	if err := f.store.WithTx(func(tx repository.TxOps) error {
		return tx.UpdatePartStock(f.partID, 10)
	}); err != nil {
		t.Fatal(err)
	}

	err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{
		ID: f.batchID, Status: 2, Operator: "测试",
	})
	if err == nil || !strings.Contains(err.Error(), "临时需要") {
		t.Fatalf("完成应当被拒并说明临时零件缺口：%v", err)
	}
	// 批次不得变成已完成。
	var st int
	if err := f.store.WithTx(func(tx repository.TxOps) error {
		b, err := tx.GetBatchForUpdate(f.batchID)
		if err != nil || b == nil {
			return err
		}
		st = b.Status
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if st != batchStatusPending {
		t.Fatalf("缺料拒绝后批次状态 = %d，应保持待生产", st)
	}
}

// 已完成/已撤销的批次不能再改临时零件；非法数量直接拒。
func TestTempPartGuards(t *testing.T) {
	f := seedTempFixture(t, 100, 0)
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: 0}); err == nil {
		t.Fatal("0 数量应当拒绝")
	}
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.partID, Qty: -3}); err == nil {
		t.Fatal("负数应当拒绝")
	}
	if err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: f.batchID, Status: 2, Operator: "测试"}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RemoveTempPart(f.batchID, f.partID); err == nil {
		t.Fatal("已完成批次不能删临时零件")
	}
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{BatchID: f.batchID, PartID: f.otherID, Qty: 1}); err == nil {
		t.Fatal("已完成批次不能加临时零件")
	}
}

// 表单按 BOM 行给推导三件套时要存下来；半套与非法值都要拒。
func TestAddTempPartStoresDerivation(t *testing.T) {
	// 对照零件也要够扣（手填总量走同一个库存检测）。
	f := seedTempFixture(t, 100, 50)
	mode := 1
	per := 4.0
	loss := 10.0
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{
		BatchID: f.batchID, PartID: f.partID, Qty: 8,
		UseMode: &mode, Quantity: &per, LossRate: &loss,
	}); err != nil {
		t.Fatalf("带推导添加失败：%v", err)
	}
	rows, err := f.svc.ListTempParts(f.batchID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v err=%v", rows, err)
	}
	if rows[0].UseMode == nil || *rows[0].UseMode != 1 ||
		rows[0].Quantity == nil || *rows[0].Quantity != 4 ||
		rows[0].LossRate == nil || *rows[0].LossRate != 10 {
		t.Fatalf("推导字段没存对：%+v", rows[0])
	}

	// 半套：只给使用方式 → 拒
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{
		BatchID: f.batchID, PartID: f.otherID, Qty: 1, UseMode: &mode,
	}); err == nil {
		t.Fatal("推导三件套不完整应当拒绝")
	}
	// 非法使用方式 → 拒
	bad := 5
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{
		BatchID: f.batchID, PartID: f.otherID, Qty: 1,
		UseMode: &bad, Quantity: &per, LossRate: &loss,
	}); err == nil {
		t.Fatal("非法使用方式应当拒绝")
	}
	// 手填总量（不带推导）→ 允许，且列里推导为空（对照零件库存 100，够扣）
	if err := f.svc.AddTempPart(usecase.AddTempPartInput{
		BatchID: f.batchID, PartID: f.otherID, Qty: 3,
	}); err != nil {
		t.Fatalf("手填总量应当允许：%v", err)
	}
	rows, _ = f.svc.ListTempParts(f.batchID)
	for _, r := range rows {
		if r.PartID == f.otherID && r.UseMode != nil {
			t.Fatalf("手填总量不该带推导：%+v", r)
		}
	}
}

// 同一零件同时出现在 BOM 与临时零件里：必须合并成一次扣减。
//
// 逐条扣会让两次 UpdatePartStock 都基于"扣之前"的库存，后一条覆盖前一条，
// 库存少扣；而撤销按两条消耗记录回退，又会多退。这里把 15（BOM 10 + 临时 5）
// 一次扣掉，并只留一条冻结记录。
func TestTempPartSamePartAsBOMDeductsOnce(t *testing.T) {
	svc, store := newTempPartFixture(t)
	var partID, batchID int64
	if err := store.WithTx(func(tx repository.TxOps) error {
		pid, err := tx.CreateProduct(&model.Product{Code: "P-T3", Name: "同件产品", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		partID, err = tx.CreatePart(&model.Part{Code: "M-T3", Name: "同件零件", Unit: "个", WarnQty: 0, Status: 1})
		if err != nil {
			return err
		}
		// BOM 每台 1 个；计划 10 → BOM 需 10
		if _, err := tx.CreateBOMItem(&model.BOMItem{ProductID: pid, PartID: partID, Quantity: 1}); err != nil {
			return err
		}
		batchID, err = tx.CreateBatch(&model.ProductBatch{BatchNo: "B-T3", ProductID: pid, PlanQty: 10, Status: batchStatusPending})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// 库存经入库建立（v17 起扣料要按批次扣）。
	stockPart(t, svc, partID, 20)

	// 再加临时 5：合计 15 ≤ 库存 20，允许。
	if err := svc.AddTempPart(usecase.AddTempPartInput{BatchID: batchID, PartID: partID, Qty: 5}); err != nil {
		t.Fatalf("AddTempPart: %v", err)
	}
	res, err := svc.StockProjection(usecase.StockProjectionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Parts) != 1 || res.Parts[0].Committed != 15 || res.Parts[0].After != 5 {
		t.Fatalf("projection = %+v，期望 committed=15 after=5", res.Parts)
	}

	if err := svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: batchID, Status: 2, Operator: "测试"}); err != nil {
		t.Fatalf("完成应当成功：%v", err)
	}
	part, err := store.GetPart(partID)
	if err != nil {
		t.Fatal(err)
	}
	if part.StockQty != 5 {
		t.Fatalf("库存 = %v，期望 5（一次扣 15，不是 10 或 20）", part.StockQty)
	}
	var rows []model.BatchConsumption
	if err := store.WithTx(func(tx repository.TxOps) error {
		var err error
		rows, err = tx.ListBatchConsumptions(batchID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ConsumedQty != 15 {
		t.Fatalf("消耗记录 = %+v，期望一条 15", rows)
	}

	// 撤销按这一条回退到 20（若留下两条会退成 25）。
	if err := svc.RevokeBatch(usecase.RevokeBatchInput{ID: batchID, Operator: "测试"}); err != nil {
		t.Fatal(err)
	}
	part, _ = store.GetPart(partID)
	if part.StockQty != 20 {
		t.Fatalf("撤销后库存 = %v，期望 20", part.StockQty)
	}
}
