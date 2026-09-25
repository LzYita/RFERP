package service_test

import (
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
