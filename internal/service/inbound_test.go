package service

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"
	"app/internal/usecase"
)

// 入库批次是库存的"来源账"：每次入库自动生成批次号并落一行，
// 详情页靠它回答"这些库存是哪几笔入库攒出来的"。这里用真实 SQLite
// 把记账、回显、备注、排序和删除级联都钉住。
func newInboundFixture(t *testing.T) (*Service, repository.Store) {
	t.Helper()
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "inbound.db"))
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

// 直接经仓储建一个带库存的零件：服务层 CreatePart 已拒绝带库存创建，
// 库存只能来自入库（或测试里的仓储直写）。
func seedStockedPart(t *testing.T, store repository.Store, code string, stock float64) int64 {
	t.Helper()
	var id int64
	err := store.WithTx(func(tx repository.TxOps) error {
		var err error
		id, err = tx.CreatePart(&model.Part{Code: code, Name: code + " 名", Unit: "个", StockQty: stock, WarnQty: 0, Status: 1})
		return err
	})
	if err != nil {
		t.Fatalf("seed part %s: %v", code, err)
	}
	return id
}

// stockPart 通过「入库」给零件备货。
//
// v17 起库存必须来自入库批次（总量 = 批次剩余之和），直接在
// CreatePart 里写字面库存会让账本从第一笔起就对不上。
func stockPart(t *testing.T, svc *Service, partID int64, qty float64) {
	t.Helper()
	if qty <= 0 {
		return
	}
	if _, err := svc.StockIn(usecase.StockInInput{PartID: partID, Qty: qty, Operator: "fixture"}); err != nil {
		t.Fatalf("stock in part %d: %v", partID, err)
	}
}

func partStock(t *testing.T, svc *Service, partID int64) float64 {
	t.Helper()
	p, err := svc.GetPart(partID)
	if err != nil || p == nil {
		t.Fatalf("get part: v=%+v err=%v", p, err)
	}
	return p.StockQty
}

func TestStockInRecordsBatchNoAndUpdatesStock(t *testing.T) {
	svc, _ := newInboundFixture(t)
	partID := seedStockedPart(t, svc.repo, "IN-1", 3)

	batchNo, err := svc.StockIn(usecase.StockInInput{PartID: partID, Qty: 2, Note: "  补货  ", Operator: "仓管甲"})
	if err != nil {
		t.Fatalf("stock in: %v", err)
	}
	if !regexp.MustCompile(`^RK-\d{8}-\d{4,}$`).MatchString(batchNo) {
		t.Fatalf("batch no %q does not match RK-YYYYMMDD-NNNN", batchNo)
	}
	if got := partStock(t, svc, partID); got != 5 {
		t.Fatalf("stock after in = %v, want 5", got)
	}

	rows, err := svc.ListPartInbounds(partID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("list inbounds = %d rows, err=%v, want 1", len(rows), err)
	}
	r := rows[0]
	if r.BatchNo != batchNo || r.Qty != 2 {
		t.Fatalf("row = %+v, want batch %s qty 2", r, batchNo)
	}
	if r.Note == nil || *r.Note != "补货" {
		t.Fatalf("note = %v, want trimmed 补货", r.Note)
	}

	// 第二笔入库：列表倒序（新的在前），批次号不重复。
	batchNo2, err := svc.StockIn(usecase.StockInInput{PartID: partID, Qty: 1.5, Operator: "仓管甲"})
	if err != nil {
		t.Fatalf("second stock in: %v", err)
	}
	if batchNo2 == batchNo {
		t.Fatalf("batch no reused: %s", batchNo2)
	}
	rows, err = svc.ListPartInbounds(partID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("list inbounds = %d rows, err=%v, want 2", len(rows), err)
	}
	if rows[0].BatchNo != batchNo2 || rows[0].Note != nil {
		t.Fatalf("newest row = %+v, want batch %s with empty note", rows[0], batchNo2)
	}
}

func TestCreatePartRejectsPresetStock(t *testing.T) {
	svc, _ := newInboundFixture(t)
	operator := "管理员"

	if _, err := svc.CreatePart(&model.Part{Code: "IN-REJ", Name: "带库存", Unit: "个", StockQty: 10, Status: 1, Operator: &operator}); err == nil {
		t.Fatal("CreatePart accepted preset stock")
	} else if !strings.Contains(err.Error(), "入库") {
		t.Fatalf("error should point to 入库, got %v", err)
	}

	created, err := svc.CreatePart(&model.Part{Code: "IN-OK", Name: "零库存起步", Unit: "个", Status: 1, Operator: &operator})
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if created.StockQty != 0 {
		t.Fatalf("new part stock = %v, want 0", created.StockQty)
	}
	rows, err := svc.ListPartInbounds(created.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("new part should have no inbound rows, got %d err=%v", len(rows), err)
	}
}

func TestUpdatePartKeepsStockFrozen(t *testing.T) {
	svc, _ := newInboundFixture(t)
	partID := seedStockedPart(t, svc.repo, "IN-2", 7)
	orig, err := svc.GetPart(partID)
	if err != nil || orig == nil {
		t.Fatalf("get part: %v", err)
	}

	// 请求里带一个"错的"库存（比如界面加载后别人入库过）：以库中现值为准。
	p := *orig
	p.Name = "改名"
	p.StockQty = 0
	op := "管理员"
	p.Operator = &op
	updated, err := svc.UpdatePart(&p)
	if err != nil {
		t.Fatalf("update part: %v", err)
	}
	if updated.Name != "改名" {
		t.Fatalf("name not updated: %q", updated.Name)
	}
	if got := partStock(t, svc, partID); got != 7 {
		t.Fatalf("stock changed by edit: %v, want 7", got)
	}
}

func TestPartInboundCascadesOnPartDelete(t *testing.T) {
	svc, _ := newInboundFixture(t)
	partID := seedStockedPart(t, svc.repo, "IN-3", 50)
	if _, err := svc.StockIn(usecase.StockInInput{PartID: partID, Qty: 5, Operator: "仓管甲"}); err != nil {
		t.Fatalf("stock in: %v", err)
	}

	// 只有入库痕迹的零件可以删（BOM/追溯/批次引用守卫都不拦），
	// 入库台账随之级联消失——零件没了，它的入库记录没有独立意义。
	if err := svc.DeletePart(partID, "管理员"); err != nil {
		t.Fatalf("delete part: %v", err)
	}
	rows, err := svc.ListPartInbounds(partID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("inbound rows after delete = %d err=%v, want 0", len(rows), err)
	}
}
