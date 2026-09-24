package repository

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"app/internal/migrate"
	"app/internal/model"
)

// Shared Store contract cases (D-013 / D-015). Run against SQLite file stores
// always, and against MySQL when RFERP_TEST_DSN is set (second adapter).

type storeFactory func(t *testing.T) (Store, string) // store, dbPath ("" if N/A)

func contractCases(t *testing.T, open storeFactory) {
	t.Run("catalog_create_read", func(t *testing.T) {
		s, _ := open(t)
		pid, err := withTxRet(s, func(tx TxOps) (int64, error) {
			return tx.CreateProduct(&model.Product{Code: "P1", Name: "Product", Unit: "个", Status: 1})
		})
		if err != nil || pid == 0 {
			t.Fatalf("create product: id=%d err=%v", pid, err)
		}
		partID, err := withTxRet(s, func(tx TxOps) (int64, error) {
			return tx.CreatePart(&model.Part{Code: "C1", Name: "Part", Unit: "个", Status: 1})
		})
		if err != nil || partID == 0 {
			t.Fatalf("create part: %v", err)
		}
		if _, err := withTxRet(s, func(tx TxOps) (int64, error) {
			return tx.CreateBOMItem(&model.BOMItem{ProductID: pid, PartID: partID, Quantity: 2, LossRate: 0})
		}); err != nil {
			t.Fatalf("create bom: %v", err)
		}
		boms, err := s.GetBOMByProduct(pid)
		if err != nil || len(boms) != 1 {
			t.Fatalf("boms=%v err=%v", boms, err)
		}
	})

	t.Run("unique_conflict_on_product_code", func(t *testing.T) {
		s, _ := open(t)
		_, err := withTxRet(s, func(tx TxOps) (int64, error) {
			return tx.CreateProduct(&model.Product{Code: "DUP", Name: "A", Unit: "u", Status: 1})
		})
		if err != nil {
			t.Fatalf("first create: %v", err)
		}
		_, err = withTxRet(s, func(tx TxOps) (int64, error) {
			return tx.CreateProduct(&model.Product{Code: "DUP", Name: "B", Unit: "u", Status: 1})
		})
		if err == nil {
			t.Fatal("expected unique conflict")
		}
		if !errors.Is(TranslateError(err), ErrUniqueConflict) {
			t.Fatalf("err=%v, want ErrUniqueConflict", err)
		}
	})

	t.Run("stock_audit_atomic_tx", func(t *testing.T) {
		s, _ := open(t)
		partID, err := withTxRet(s, func(tx TxOps) (int64, error) {
			return tx.CreatePart(&model.Part{Code: "S1", Name: "Part", Unit: "u", Status: 1})
		})
		if err != nil {
			t.Fatal(err)
		}
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
			t.Fatalf("tx: %v", err)
		}
		got, err := s.GetPart(partID)
		if err != nil || got == nil || got.StockQty != 5 {
			t.Fatalf("part=%v err=%v", got, err)
		}
		logs, err := s.ListAuditLogs("parts", partID)
		if err != nil || len(logs) == 0 {
			t.Fatalf("audit logs=%v err=%v", logs, err)
		}
	})

	t.Run("skip_part_idempotent", func(t *testing.T) {
		s, _ := open(t)
		pid, _ := withTxRet(s, func(tx TxOps) (int64, error) {
			return tx.CreateProduct(&model.Product{Code: "BP", Name: "P", Unit: "u", Status: 1})
		})
		partID, _ := withTxRet(s, func(tx TxOps) (int64, error) {
			return tx.CreatePart(&model.Part{Code: "BP-C", Name: "C", Unit: "u", Status: 1})
		})
		err := s.WithTx(func(tx TxOps) error {
			bid, err := tx.CreateBatch(&model.ProductBatch{BatchNo: "B1", ProductID: pid, PlanQty: 1, Status: 0})
			if err != nil {
				return err
			}
			if err := tx.AddSkipPart(bid, partID); err != nil {
				return err
			}
			return tx.AddSkipPart(bid, partID)
		})
		if err != nil {
			t.Fatalf("skip parts: %v", err)
		}
	})

	t.Run("user_admin_counts", func(t *testing.T) {
		s, _ := open(t)
		if _, err := s.CreateUser(&model.User{Username: "admin", PasswordHash: "x", Role: "admin", Status: 1}); err != nil {
			t.Fatal(err)
		}
		n, err := s.CountActiveAdmins()
		if err != nil || n != 1 {
			t.Fatalf("admins=%d err=%v", n, err)
		}
	})
}

func TestSQLiteStoreContract(t *testing.T) {
	contractCases(t, func(t *testing.T) (Store, string) {
		path := filepath.Join(t.TempDir(), "contract.db")
		db, err := OpenSQLite(path)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if _, err := migrate.RunSQLite(db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		return NewSQLite(db), path
	})
}

// TestMySQLStoreContract runs the same contract cases against the MySQL
// adapter (D-013 one set of business semantics). Requires RFERP_TEST_DSN
// pointing at a disposable database — the suite migrates and writes to it.
func TestMySQLStoreContract(t *testing.T) {
	dsn := os.Getenv("RFERP_TEST_DSN")
	if dsn == "" {
		t.Skip("RFERP_TEST_DSN not set; skipping MySQL Store contract")
	}
	contractCases(t, func(t *testing.T) (Store, string) {
		db, err := sqlx.Connect("mysql", dsn)
		if err != nil {
			t.Fatalf("connect mysql: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		if _, err := migrate.Run(db, migrate.Options{DSN: dsn}); err != nil {
			t.Fatalf("migrate mysql: %v", err)
		}
		return New(db), ""
	})
}

func withTxRet(s Store, fn func(TxOps) (int64, error)) (int64, error) {
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
