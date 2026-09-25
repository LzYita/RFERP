package ui

import (
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"app/internal/migrate"
	"app/internal/model"
	"app/internal/repository"
	"app/internal/service"
)

// 回归：审计页的每一个单元格都必须能渲染。
//
// 背景：makeCellTmpl 改成返回 *cellWidget 之后，audit.go 的 updateCellWrap 仍在
// 无保护地断言 *fyne.Container，于是打开「操作记录」时 panic、进程直接退出。
// 这里直接驱动 Table 的 CreateCell/UpdateCell，覆盖包括「变更明细」在内的所有列。
func TestAuditScreenRendersEveryCell(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	db, err := repository.OpenSQLite(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := migrate.RunSQLite(db, migrate.SQLiteOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := repository.NewSQLite(db)

	oldData := map[string]any{"code": "P-1", "name": "Part", "status": float64(1)}
	newData := map[string]any{"in_qty": float64(3), "old_stock": float64(1), "new_stock": float64(4)}
	operator := "tester"
	if err := store.CreateAuditLog(&model.AuditLog{
		TableName: "parts", RecordID: 1, Action: "STOCK_IN",
		OldData: &oldData, NewData: &newData, Operator: &operator,
	}); err != nil {
		t.Fatalf("seed audit: %v", err)
	}

	screen := NewAuditScreen(service.New(store, "", "", nil), nil)
	screen.Build()
	if screen.table == nil {
		t.Fatal("audit table was not built")
	}

	rows, cols := screen.table.Length()
	if rows < 2 {
		t.Fatalf("rows = %d，期望至少表头 + 1 条记录", rows)
	}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			cell := screen.table.CreateCell()
			screen.table.UpdateCell(widget.TableCellID{Row: r, Col: c}, cell)
		}
	}
}
