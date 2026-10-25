package repository

import (
	"path/filepath"
	"testing"

	"app/internal/migrate"
	"app/internal/model"
)

// 删除零件的守卫依赖这条查询：同一个零件被多个产品引用时必须全部列出来，
// 顺序稳定（按产品编码），否则界面上的提示会随机顺序变化。
func TestBOMProductsForPart(t *testing.T) {
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "bom-usage.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatal(err)
	}
	store := NewSQLite(db)

	var partID, bomB int64
	if err := store.WithTx(func(tx TxOps) error {
		productB, err := tx.CreateProduct(&model.Product{Code: "P-002", Name: "产品二", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		productA, err := tx.CreateProduct(&model.Product{Code: "P-001", Name: "产品一", Unit: "台", Status: 1})
		if err != nil {
			return err
		}
		partID, err = tx.CreatePart(&model.Part{Code: "M-001", Name: "共用零件", Unit: "个", Status: 1})
		if err != nil {
			return err
		}
		if bomB, err = tx.CreateBOMItem(&model.BOMItem{ProductID: productB, PartID: partID, Quantity: 1}); err != nil {
			return err
		}
		_, err = tx.CreateBOMItem(&model.BOMItem{ProductID: productA, PartID: partID, Quantity: 2})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	got, err := store.BOMProductsForPart(partID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Code != "P-001" || got[1].Code != "P-002" {
		t.Fatalf("引用该零件的产品 = %+v，期望按编码排序的 P-001/P-002", got)
	}
	if got[0].ID == 0 || got[0].Name == "" {
		t.Fatalf("产品信息不完整：%+v", got[0])
	}

	// 删掉一个 BOM 行后，只剩另一个产品；删光后返回空切片而不是 nil，
	// 这样 API 序列化成 [] 而不是 null（前端可以直接遍历）。
	if err := store.WithTx(func(tx TxOps) error { return tx.DeleteBOMItem(bomB) }); err != nil {
		t.Fatal(err)
	}
	got, err = store.BOMProductsForPart(partID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Code != "P-001" {
		t.Fatalf("删掉一个 BOM 行后 = %+v，期望只剩 P-001", got)
	}

	if err := store.WithTx(func(tx TxOps) error {
		items, err := tx.GetBOMByProduct(got[0].ID)
		if err != nil {
			return err
		}
		for _, it := range items {
			if err := tx.DeleteBOMItem(it.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err = store.BOMProductsForPart(partID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("没有引用时应为空切片，得到 %#v", got)
	}
}
