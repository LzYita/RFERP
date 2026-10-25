package repository

import (
	"path/filepath"
	"testing"

	"app/internal/migrate"
	"app/internal/model"
)

// 删除零件前的第二道守卫：按批次汇总投料记录。
// 汇总错了会直接误导用户（「到底哪几个批次用过它」），所以连条数一起断言。
func TestTracesForPart(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "trace-usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatal(err)
	}
	store := NewSQLite(db)

	var partID int64
	if err := store.WithTx(func(tx TxOps) error {
		product, err := tx.CreateProduct(&model.Product{Code: "P-1", Name: "产品", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		partID, err = tx.CreatePart(&model.Part{Code: "M-1", Name: "零件", Unit: "个", Status: 1})
		if err != nil {
			return err
		}
		// 批次编码故意乱序创建，断言输出按批次号排序（界面顺序要稳定）。
		for _, no := range []string{"B-002", "B-001"} {
			batchID, err := tx.CreateBatch(&model.ProductBatch{BatchNo: no, ProductID: product, PlanQty: 10})
			if err != nil {
				return err
			}
			records := 1
			if no == "B-001" {
				records = 2
			}
			for i := 0; i < records; i++ {
				if _, err := tx.CreateTrace(&model.BatchTrace{BatchID: batchID, PartID: partID, UsedQty: 1}); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.TracesForPart(partID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].BatchNo != "B-001" || got[0].Records != 2 ||
		got[1].BatchNo != "B-002" || got[1].Records != 1 {
		t.Fatalf("汇总 = %+v，期望 B-001/2 + B-002/1（按批次号排序）", got)
	}

	// 没有投料记录的零件返回空切片而不是 nil：API 序列化成 []，界面可直接遍历。
	var emptyID int64
	if err := store.WithTx(func(tx TxOps) error {
		var err error
		emptyID, err = tx.CreatePart(&model.Part{Code: "M-EMPTY", Name: "未投料零件", Unit: "个", Status: 1})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	empty, err := store.TracesForPart(emptyID)
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("没有投料记录时应为空切片，得到 %#v", empty)
	}
}
