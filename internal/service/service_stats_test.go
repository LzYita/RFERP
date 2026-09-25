package service_test

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"
	"app/internal/service"
)

func TestGetStockStatsIncludesRecentSQLiteAuditLogs(t *testing.T) {
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	store := repository.NewSQLite(db)
	oldData := map[string]any{"code": "P-1", "name": "Part", "supplier": "Supplier"}
	stockIn := map[string]any{"in_qty": float64(3)}
	if err := store.CreateAuditLog(&model.AuditLog{
		TableName: "parts", RecordID: 1, Action: "STOCK_IN",
		OldData: &oldData, NewData: &stockIn,
	}); err != nil {
		t.Fatalf("insert stock-in audit: %v", err)
	}
	stockOut := map[string]any{"deduct": float64(2)}
	if err := store.CreateAuditLog(&model.AuditLog{
		TableName: "parts", RecordID: 1, Action: "STOCK_DEDUCT",
		OldData: &oldData, NewData: &stockOut,
	}); err != nil {
		t.Fatalf("insert stock-out audit: %v", err)
	}
	// SQL backups created by the MySQL adapter can contain naive DATETIME text.
	legacyCreatedAt := time.Now().Format("2006-01-02 15:04:05")
	if _, err := db.Exec(`INSERT INTO audit_log (table_name, record_id, action, old_data, new_data, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, "parts", 1, "STOCK_IN",
		`{"code":"P-1","name":"Part","supplier":"Supplier"}`, `{"in_qty":4}`, legacyCreatedAt); err != nil {
		t.Fatalf("insert imported SQL-style audit: %v", err)
	}

	start := time.Now().AddDate(0, 0, -30)
	end := time.Now().Add(time.Second)
	logs, err := store.ListStockLogsByDate(start, end, []string{"STOCK_IN", "STOCK_DEDUCT", "STOCK_ADJUST"})
	if err != nil {
		t.Fatalf("list recent stock audit logs: %v", err)
	}
	if len(logs) != 3 {
		t.Errorf("recent stock audit logs = %d, want 3", len(logs))
	}
	auditLogs, err := store.ListAuditLogsByDate(start, end)
	if err != nil {
		t.Fatalf("list recent audit logs: %v", err)
	}
	if len(auditLogs) != 3 {
		t.Errorf("recent audit logs = %d, want 3", len(auditLogs))
	}

	stats, err := service.New(store, "", "", nil).GetStockStats(30)
	if err != nil {
		t.Fatalf("GetStockStats: %v", err)
	}
	if stats.TotalIn != 7 || stats.TotalOut != 2 {
		t.Errorf("stats totals in=%v out=%v, want 7 and 2", stats.TotalIn, stats.TotalOut)
	}
}

// Issue #40：已完成又撤销的批次，其 STOCK_DEDUCT 与撤销回退 STOCK_ADJUST
// 两端日志都不应计入汇总、每日趋势和排行；未撤销批次与无批次日志保留。
func TestGetStockStatsExcludesRevokedBatchMovements(t *testing.T) {
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	store := repository.NewSQLite(db)

	var revokedBatchID, activeBatchID int64
	if err := store.WithTx(func(tx repository.TxOps) error {
		productID, err := tx.CreateProduct(&model.Product{Code: "PR-1", Name: "Product", Unit: "个", Status: 1})
		if err != nil {
			return err
		}
		revokedBatchID, err = tx.CreateBatch(&model.ProductBatch{
			BatchNo: "B-REVOKED", ProductID: productID, PlanQty: 1, Status: 4,
			Customer: strPtr("Ghost Corp"),
		})
		if err != nil {
			return err
		}
		activeBatchID, err = tx.CreateBatch(&model.ProductBatch{
			BatchNo: "B-ACTIVE", ProductID: productID, PlanQty: 1, Status: 2,
			Customer: strPtr("Real Corp"),
		})
		return err
	}); err != nil {
		t.Fatalf("seed batches: %v", err)
	}

	oldData := map[string]any{"code": "P-1", "name": "Part", "supplier": "Supplier"}
	logs := []model.AuditLog{
		// 撤销批次：完成扣减 300 —— 排除
		{TableName: "parts", RecordID: 1, Action: "STOCK_DEDUCT", OldData: &oldData,
			NewData: &map[string]any{"deduct": float64(300), "batch_id": float64(revokedBatchID)}},
		// 撤销批次：撤销回退 +300 —— 排除
		{TableName: "parts", RecordID: 1, Action: "STOCK_ADJUST", OldData: &oldData,
			NewData: &map[string]any{"diff": float64(300), "batch_id": float64(revokedBatchID), "remark": "批次撤销回退"}},
		// 未撤销批次：正常扣减 50 —— 保留
		{TableName: "parts", RecordID: 1, Action: "STOCK_DEDUCT", OldData: &oldData,
			NewData: &map[string]any{"deduct": float64(50), "batch_id": float64(activeBatchID)}},
		// 普通入库 —— 保留
		{TableName: "parts", RecordID: 1, Action: "STOCK_IN", OldData: &oldData,
			NewData: &map[string]any{"in_qty": float64(200)}},
		// 盘点调整（无 batch_id）—— 保留
		{TableName: "parts", RecordID: 1, Action: "STOCK_ADJUST", OldData: &oldData,
			NewData: &map[string]any{"diff": float64(10)}},
	}
	for i := range logs {
		if err := store.CreateAuditLog(&logs[i]); err != nil {
			t.Fatalf("insert audit log %d: %v", i, err)
		}
	}

	stats, err := service.New(store, "", "", nil).GetStockStats(30)
	if err != nil {
		t.Fatalf("GetStockStats: %v", err)
	}
	if stats.TotalIn != 210 || stats.TotalOut != 50 {
		t.Errorf("stats totals in=%v out=%v, want 210 and 50", stats.TotalIn, stats.TotalOut)
	}

	var dayIn, dayOut float64
	for _, d := range stats.Days {
		dayIn += d.In
		dayOut += d.Out
	}
	if dayIn != 210 || dayOut != 50 {
		t.Errorf("daily trend sums in=%v out=%v, want 210 and 50", dayIn, dayOut)
	}

	if len(stats.TopOut) != 1 || stats.TopOut[0].Out != 50 {
		t.Errorf("TopOut = %+v, want single part with out=50", stats.TopOut)
	}
	if len(stats.TopCustomers) != 1 || stats.TopCustomers[0].Name != "Real Corp" || stats.TopCustomers[0].Out != 50 {
		t.Errorf("TopCustomers = %+v, want only Real Corp with out=50", stats.TopCustomers)
	}
	if len(stats.TopProducts) != 1 || stats.TopProducts[0].Out != 50 {
		t.Errorf("TopProducts = %+v, want single product with out=50", stats.TopProducts)
	}
	if stats.ProductOutTotal != 50 {
		t.Errorf("ProductOutTotal = %v, want 50", stats.ProductOutTotal)
	}
}

func strPtr(s string) *string { return &s }

type failingBatchListStore struct {
	repository.Store
	err error
}

func (s failingBatchListStore) ListBatches() ([]model.ProductBatch, error) {
	return nil, s.err
}

func TestGetStockStatsPropagatesBatchLookupError(t *testing.T) {
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}

	store := repository.NewSQLite(db)
	data := map[string]any{"batch_id": float64(1), "deduct": float64(3)}
	if err := store.CreateAuditLog(&model.AuditLog{
		TableName: "parts", RecordID: 1, Action: "STOCK_DEDUCT", NewData: &data,
	}); err != nil {
		t.Fatalf("insert audit log: %v", err)
	}
	lookupErr := errors.New("batch lookup unavailable")
	_, err = service.New(failingBatchListStore{Store: store, err: lookupErr}, "", "", nil).GetStockStats(30)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("GetStockStats error = %v, want batch lookup error", err)
	}
}

func TestGetStockStatsExcludesRevokedBatchWhenDeductionIsOutsideWindow(t *testing.T) {
	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate sqlite: %v", err)
	}
	store := repository.NewSQLite(db)
	var batchID int64
	if err := store.WithTx(func(tx repository.TxOps) error {
		productID, err := tx.CreateProduct(&model.Product{Code: "PR-2", Name: "Product", Unit: "个", Status: 1})
		if err != nil {
			return err
		}
		batchID, err = tx.CreateBatch(&model.ProductBatch{BatchNo: "B-OLD-RETURN", ProductID: productID, PlanQty: 1, Status: 4})
		return err
	}); err != nil {
		t.Fatalf("seed revoked batch: %v", err)
	}
	oldData := map[string]any{"code": "P-1", "name": "Part"}
	orphan := map[string]any{"batch_id": float64(999999), "deduct": float64(7)}
	if err := store.CreateAuditLog(&model.AuditLog{
		TableName: "parts", RecordID: 1, Action: "STOCK_DEDUCT", OldData: &oldData, NewData: &orphan,
	}); err != nil {
		t.Fatalf("insert orphan stock deduction: %v", err)
	}
	stockReturn := map[string]any{"batch_id": float64(batchID), "diff": float64(5)}
	if err := store.CreateAuditLog(&model.AuditLog{
		TableName: "parts", RecordID: 1, Action: "STOCK_ADJUST", OldData: &oldData, NewData: &stockReturn,
	}); err != nil {
		t.Fatalf("insert stock return: %v", err)
	}
	oldDeductionTime := time.Now().AddDate(0, 0, -40).UTC().Format("2006-01-02T15:04:05.000Z")
	if _, err := db.Exec(`INSERT INTO audit_log (table_name, record_id, action, new_data, created_at)
		VALUES (?, ?, ?, ?, ?)`, "parts", 1, "STOCK_DEDUCT",
		`{"batch_id":`+fmt.Sprint(batchID)+`,"deduct":5}`, oldDeductionTime); err != nil {
		t.Fatalf("insert old stock deduction: %v", err)
	}
	stats, err := service.New(store, "", "", nil).GetStockStats(30)
	if err != nil {
		t.Fatalf("GetStockStats: %v", err)
	}
	if stats.TotalIn != 0 || stats.TotalOut != 7 {
		t.Fatalf("stats totals in=%v out=%v, want 0 and 7", stats.TotalIn, stats.TotalOut)
	}
}
