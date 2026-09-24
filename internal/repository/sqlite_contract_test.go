package repository

import (
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"app/internal/migrate"
	"app/internal/model"
)

func openContractSQLite(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contract.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewSQLite(db)
}

func TestSQLiteStoreImplementsContract(t *testing.T) {
	s := openContractSQLite(t)

	// Catalog
	pid, err := s.WithTxRet(func(tx TxOps) (int64, error) {
		return tx.CreateProduct(&model.Product{Code: "P1", Name: "Product", Unit: "个", Status: 1})
	})
	if err != nil || pid == 0 {
		t.Fatalf("create product: id=%d err=%v", pid, err)
	}
	partID, err := s.WithTxRet(func(tx TxOps) (int64, error) {
		return tx.CreatePart(&model.Part{Code: "C1", Name: "Part", Unit: "个", StockQty: 0, Status: 1})
	})
	if err != nil || partID == 0 {
		t.Fatalf("create part: id=%d err=%v", partID, err)
	}
	if _, err := s.WithTxRet(func(tx TxOps) (int64, error) {
		return tx.CreateBOMItem(&model.BOMItem{ProductID: pid, PartID: partID, Quantity: 2, LossRate: 0})
	}); err != nil {
		t.Fatalf("create bom: %v", err)
	}
	boms, err := s.GetBOMByProduct(pid)
	if err != nil || len(boms) != 1 {
		t.Fatalf("boms=%v err=%v", boms, err)
	}

	// Inventory with lock + audit in one tx
	err = s.WithTx(func(tx TxOps) error {
		p, err := tx.GetPartForUpdate(partID)
		if err != nil || p == nil {
			return err
		}
		if err := tx.UpdatePartStock(partID, 5); err != nil {
			return err
		}
		return tx.CreateAuditLog(&model.AuditLog{
			TableName: "parts", RecordID: partID, Action: "STOCK_IN",
			Operator: strp("tester"),
		})
	})
	if err != nil {
		t.Fatalf("stock tx: %v", err)
	}
	got, err := s.GetPart(partID)
	if err != nil || got == nil || got.StockQty != 5 {
		t.Fatalf("part=%v err=%v", got, err)
	}

	// Users
	uid, err := s.CreateUser(&model.User{Username: "admin", PasswordHash: "x", Role: "admin", Status: 1})
	if err != nil || uid == 0 {
		t.Fatalf("create user: %v", err)
	}
	n, err := s.CountActiveAdmins()
	if err != nil || n != 1 {
		t.Fatalf("admins=%d err=%v", n, err)
	}

	// Unique constraint on skip parts + INSERT OR IGNORE
	if err := s.WithTx(func(tx TxOps) error {
		bid, err := tx.CreateBatch(&model.ProductBatch{BatchNo: "B1", ProductID: pid, PlanQty: 1, Status: 0})
		if err != nil {
			return err
		}
		if err := tx.AddSkipPart(bid, partID); err != nil {
			return err
		}
		return tx.AddSkipPart(bid, partID) // idempotent
	}); err != nil {
		t.Fatalf("skip parts: %v", err)
	}
}

// WithTxRet is a small test helper so contract tests can capture generated IDs.
func (s *SQLiteStore) WithTxRet(fn func(TxOps) (int64, error)) (int64, error) {
	var id int64
	err := s.WithTx(func(tx TxOps) error {
		n, err := fn(tx)
		if err != nil {
			return err
		}
		id = n
		return nil
	})
	return id, err
}

func strp(s string) *string { return &s }

func TestSQLiteVacuumIntoSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "src.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	if _, err := migrate.RunSQLite(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := NewSQLite(db)
	if _, err := store.CreateUser(&model.User{Username: "u", PasswordHash: "h", Role: "viewer", Status: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	saveDir := filepath.Join(t.TempDir(), "backups")
	out, err := SnapshotSQLiteFile(path, saveDir)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if out == "" {
		t.Fatal("empty snapshot path")
	}
	// Open snapshot and verify data landed.
	sdb, err := OpenSQLite(out)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer sdb.Close()
	var n int
	if err := sdb.Get(&n, `SELECT COUNT(*) FROM users`); err != nil || n != 1 {
		t.Fatalf("snapshot users=%d err=%v", n, err)
	}
}

var _ = sqlx.DB{}
