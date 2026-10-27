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

// 删除零件前必须说清它还挂在哪个批次的哪种用途上：临时零件/跳过零件/完成扣料。
// 三张表都有 parts 外键且不级联，不拦就只剩 FOREIGN KEY constraint failed。
func TestDeletePartRefusesBatchReferences(t *testing.T) {
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "delete.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := repository.NewSQLite(db)
	svc := NewWithSnapshot(store, nil, nil)

	var tempPart, skipPart, consumedPart, bomID, consumedBatch int64
	if err := store.WithTx(func(tx repository.TxOps) error {
		pid, err := tx.CreateProduct(&model.Product{Code: "P-DEL", Name: "删除守卫产品", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		tempPart, err = tx.CreatePart(&model.Part{Code: "M-TMP", Name: "临时引用件", Unit: "个", StockQty: 100, Status: 1})
		if err != nil {
			return err
		}
		skipPart, err = tx.CreatePart(&model.Part{Code: "M-SKP", Name: "跳过引用件", Unit: "个", StockQty: 100, Status: 1})
		if err != nil {
			return err
		}
		consumedPart, err = tx.CreatePart(&model.Part{Code: "M-CON", Name: "扣料引用件", Unit: "个", StockQty: 100, Status: 1})
		if err != nil {
			return err
		}
		// 临时：一个待生产批次 + 临时零件
		tempBatch, err := tx.CreateBatch(&model.ProductBatch{BatchNo: "B-TMP", ProductID: pid, PlanQty: 1, Status: batchStatusPending})
		if err != nil {
			return err
		}
		if err := tx.AddTempPart(&model.BatchTempPart{BatchID: tempBatch, PartID: tempPart, Qty: 5}); err != nil {
			return err
		}
		// 跳过：另一个待生产批次
		skipBatch, err := tx.CreateBatch(&model.ProductBatch{BatchNo: "B-SKP", ProductID: pid, PlanQty: 1, Status: batchStatusPending})
		if err != nil {
			return err
		}
		if err := tx.AddSkipPart(skipBatch, skipPart); err != nil {
			return err
		}
		// 完成扣料：BOM 引用 → 完成 → 再删 BOM 条目，只剩冻结消耗记录
		bomID, err = tx.CreateBOMItem(&model.BOMItem{ProductID: pid, PartID: consumedPart, Quantity: 1})
		if err != nil {
			return err
		}
		consumedBatch, err = tx.CreateBatch(&model.ProductBatch{BatchNo: "B-CON", ProductID: pid, PlanQty: 1, Status: batchStatusPending})
		return err
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.UpdateBatchStatus(usecase.UpdateBatchStatusInput{ID: consumedBatch, Status: 2, Operator: "测试"}); err != nil {
		t.Fatalf("complete consumed batch: %v", err)
	}
	if err := svc.RemoveBOMItem(bomID, "测试"); err != nil {
		t.Fatalf("remove bom: %v", err)
	}

	cases := []struct {
		name   string
		partID int64
		want   []string
	}{
		{"临时零件引用", tempPart, []string{"B-TMP", "临时零件"}},
		{"跳过零件引用", skipPart, []string{"B-SKP", "跳过零件"}},
		{"完成扣料引用", consumedPart, []string{"B-CON", "完成扣料"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := svc.DeletePart(c.partID, "测试")
			if err == nil {
				t.Fatal("应当拒绝删除")
			}
			for _, want := range c.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("错误文案缺少 %q：%s", want, err)
				}
			}
		})
	}
}
