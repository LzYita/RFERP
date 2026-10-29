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

// 逐批次盘点与批次扣料的账本不变量：
//
//	parts.stock_qty == Σ part_inbounds.remaining
//
// 这里用真实 SQLite 把三条会被它绑住的路径都钉一遍：
//  1. 完成生产按先进先出扣批次，撤销按原批次还回去；
//  2. 逐批次盘点改剩余并同步总量；
//  3. 总量盘点的差额也落回批次（盘亏扣旧批、盘盈补一笔可指认的批次）。
func newInboundCountFixture(t *testing.T) (*Service, repository.Store) {
	t.Helper()
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "count.db"))
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

type countFixture struct {
	svc     *Service
	store   repository.Store
	partID  int64
	product int64
	batchID int64
}

// seedCountFixture 一个零件 + 一笔 BOM（每台 1 个）+ 一个待生产批次，
// 库存由调用方通过入库建立。
func seedCountFixture(t *testing.T, planQty int) *countFixture {
	t.Helper()
	svc, store := newInboundCountFixture(t)
	f := &countFixture{svc: svc, store: store}
	if err := store.WithTx(func(tx repository.TxOps) error {
		pid, err := tx.CreateProduct(&model.Product{Code: "P-CNT", Name: "盘点验证产品", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		f.product = pid
		partID, err := tx.CreatePart(&model.Part{Code: "M-CNT", Name: "盘点验证零件", Unit: "个", Status: 1})
		if err != nil {
			return err
		}
		f.partID = partID
		if _, err := tx.CreateBOMItem(&model.BOMItem{ProductID: pid, PartID: partID, Quantity: 1}); err != nil {
			return err
		}
		id, err := tx.CreateBatch(&model.ProductBatch{BatchNo: "B-CNT", ProductID: pid, PlanQty: planQty, Status: 0})
		if err != nil {
			return err
		}
		f.batchID = id
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return f
}

func (f *countFixture) inbounds(t *testing.T) []model.PartInbound {
	t.Helper()
	rows, err := f.svc.ListPartInbounds(f.partID)
	if err != nil {
		t.Fatalf("list inbounds: %v", err)
	}
	// 列表是倒序的；测试里关心的是先进先出顺序，转回正序。
	out := make([]model.PartInbound, len(rows))
	for i, r := range rows {
		out[len(rows)-1-i] = r
	}
	return out
}

func (f *countFixture) assertLedger(t *testing.T, wantRemaining []float64) {
	t.Helper()
	rows := f.inbounds(t)
	if len(rows) != len(wantRemaining) {
		t.Fatalf("批次 = %d 笔，期望 %d：%+v", len(rows), len(wantRemaining), rows)
	}
	var sum float64
	for i, want := range wantRemaining {
		if rows[i].Remaining != want {
			t.Fatalf("第 %d 批剩余 = %v，期望 %v（全部：%+v）", i+1, rows[i].Remaining, want, rows)
		}
		sum += rows[i].Remaining
	}
	part, err := f.svc.GetPart(f.partID)
	if err != nil || part == nil {
		t.Fatalf("get part: %v", err)
	}
	if part.StockQty != sum {
		t.Fatalf("库存 %v ≠ 批次剩余之和 %v（账本与总量失配）", part.StockQty, sum)
	}
}

func TestCompletionDrawsFIFOAndRevokeRestoresOriginalBatches(t *testing.T) {
	f := seedCountFixture(t, 15)
	stockPart(t, f.svc, f.partID, 10)
	stockPart(t, f.svc, f.partID, 10)
	f.assertLedger(t, []float64{10, 10})

	recordBatchTracesForTest(t, f.svc, f.batchID)
	if err := f.svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: f.batchID, Status: 2, Operator: "生产"}); err != nil {
		t.Fatalf("complete: %v", err)
	}
	// 需要 15：先吃光第 1 批，再从第 2 批扣 5。
	f.assertLedger(t, []float64{0, 5})

	if err := f.svc.RevokeBatch(usecase.RevokeBatchInput{ID: f.batchID, Operator: "生产"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// 撤销不能笼统加回总量，要还回原来那两批。
	f.assertLedger(t, []float64{10, 10})
}

func TestAdjustInboundsPerBatchKeepsTotalInSync(t *testing.T) {
	f := seedCountFixture(t, 1)
	stockPart(t, f.svc, f.partID, 10)
	stockPart(t, f.svc, f.partID, 5)
	rows := f.inbounds(t)

	// 现场数出来：第 1 批少了 2，第 2 批多出 1 → 总量 15 → 14。
	if err := f.svc.AdjustInbounds(usecase.AdjustInboundsInput{
		PartID: f.partID,
		Items: []usecase.InboundCount{
			{InboundID: rows[0].ID, Remaining: 8},
			{InboundID: rows[1].ID, Remaining: 6},
		},
		Operator: "仓管",
	}); err != nil {
		t.Fatalf("adjust inbounds: %v", err)
	}
	f.assertLedger(t, []float64{8, 6})

	// 盘到 0 是合法结果：批次"已用完"，仍留在台账里。
	if err := f.svc.AdjustInbounds(usecase.AdjustInboundsInput{
		PartID:   f.partID,
		Items:    []usecase.InboundCount{{InboundID: rows[0].ID, Remaining: 0}},
		Operator: "仓管",
	}); err != nil {
		t.Fatalf("adjust to zero: %v", err)
	}
	f.assertLedger(t, []float64{0, 6})
}

func TestAdjustInboundsRejectsForeignOrMissingBatch(t *testing.T) {
	f := seedCountFixture(t, 1)
	stockPart(t, f.svc, f.partID, 10)
	// 同一库里再建一个零件：批次 id 属于这个库，"不属于本零件"才是要验的条件。
	var otherPart int64
	if err := f.store.WithTx(func(tx repository.TxOps) error {
		var err error
		otherPart, err = tx.CreatePart(&model.Part{Code: "M-CNT-2", Name: "另一个零件", Unit: "个", Status: 1})
		return err
	}); err != nil {
		t.Fatalf("seed other part: %v", err)
	}
	stockPart(t, f.svc, otherPart, 10)
	otherRows, err := f.svc.ListPartInbounds(otherPart)
	if err != nil || len(otherRows) != 1 {
		t.Fatalf("other inbounds: %v %v", otherRows, err)
	}
	foreign := otherRows[0]

	err = f.svc.AdjustInbounds(usecase.AdjustInboundsInput{
		PartID:   f.partID,
		Items:    []usecase.InboundCount{{InboundID: foreign.ID, Remaining: 1}},
		Operator: "仓管",
	})
	if err == nil || !strings.Contains(err.Error(), "不属于") {
		t.Fatalf("别的零件的批次必须被拒，实际：%v", err)
	}
	if err := f.svc.AdjustInbounds(usecase.AdjustInboundsInput{
		PartID:   f.partID,
		Items:    []usecase.InboundCount{{InboundID: 99999, Remaining: 1}},
		Operator: "仓管",
	}); err == nil {
		t.Fatal("不存在的批次必须被拒")
	}
	// 拒绝时不能留下半截修改：两个零件都保持原样。
	f.assertLedger(t, []float64{10})
	otherAfter, err := f.svc.ListPartInbounds(otherPart)
	if err != nil || len(otherAfter) != 1 || otherAfter[0].Remaining != 10 {
		t.Fatalf("别的零件被改动了：%+v err=%v", otherAfter, err)
	}
}

func TestAdjustInboundsRejectsDuplicateAndNegative(t *testing.T) {
	f := seedCountFixture(t, 1)
	stockPart(t, f.svc, f.partID, 10)
	row := f.inbounds(t)[0]

	if err := f.svc.AdjustInbounds(usecase.AdjustInboundsInput{
		PartID: f.partID,
		Items: []usecase.InboundCount{
			{InboundID: row.ID, Remaining: 5},
			{InboundID: row.ID, Remaining: 6},
		},
		Operator: "仓管",
	}); err == nil {
		t.Fatal("同一批次重复出现必须被拒")
	}
	if err := f.svc.AdjustInbounds(usecase.AdjustInboundsInput{
		PartID:   f.partID,
		Items:    []usecase.InboundCount{{InboundID: row.ID, Remaining: -1}},
		Operator: "仓管",
	}); err == nil {
		t.Fatal("负数实盘必须被拒")
	}
	f.assertLedger(t, []float64{10})
}

func TestGlobalAdjustStockKeepsBatchLedgerInSync(t *testing.T) {
	f := seedCountFixture(t, 1)
	stockPart(t, f.svc, f.partID, 10)
	stockPart(t, f.svc, f.partID, 10)

	// 盘盈到 25：多出的 5 要有可指认的来源批次。
	if err := f.svc.AdjustStock(usecase.AdjustStockInput{PartID: f.partID, NewQty: 25, Operator: "仓管"}); err != nil {
		t.Fatalf("adjust up: %v", err)
	}
	f.assertLedger(t, []float64{10, 10, 5})
	rows := f.inbounds(t)
	if rows[2].Note == nil || *rows[2].Note != "盘点盈余" {
		t.Fatalf("盈余批次备注 = %v，期望「盘点盈余」", rows[2].Note)
	}

	// 盘亏到 4：先进先出扣，剩最后一批 4。
	if err := f.svc.AdjustStock(usecase.AdjustStockInput{PartID: f.partID, NewQty: 4, Operator: "仓管"}); err != nil {
		t.Fatalf("adjust down: %v", err)
	}
	f.assertLedger(t, []float64{0, 0, 4})
}
