package migrate

import (
	"regexp"
	"testing"
)

// v16 的旧版适配：升级前零件库存是表单里直接填的，没有入库痕迹。
// 种子把每个非零库存整体记成一笔"历史库存初始化"，且重复执行不重复播种
// （迁移失败重跑时步骤未记版本，会从头再跑一遍）。
func TestPartInboundsSeedLegacyStock(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := RunSQLite(db, SQLiteOptions{}); err != nil {
		t.Fatalf("migrate fresh db: %v", err)
	}

	// 空库播种后不应有记录；随后直接插三档库存零件再手动跑一次种子，
	// 模拟"带着旧版数据升级"的库。
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM part_inbounds`); err != nil {
		t.Fatalf("count on fresh db: %v", err)
	}
	if n != 0 {
		t.Fatalf("fresh db seeded %d rows, want 0", n)
	}

	for _, p := range []struct {
		code  string
		stock float64
	}{
		{"LEG-A", 100}, {"LEG-ZERO", 0}, {"LEG-FRAC", 12.5},
	} {
		if _, err := db.Exec(`INSERT INTO parts (code, name, unit, stock_qty, warn_qty, status) VALUES (?, ?, '个', ?, 0, 1)`,
			p.code, p.code, p.stock); err != nil {
			t.Fatalf("seed part %s: %v", p.code, err)
		}
	}

	if err := applyPartInboundsSQLite(db); err != nil {
		t.Fatalf("seed: %v", err)
	}
	assertInboundSeed(t, db)

	// 再跑一遍：已有种子的零件不重复记，批次号也保持回填后的样子。
	if err := applyPartInboundsSQLite(db); err != nil {
		t.Fatalf("re-seed (idempotent): %v", err)
	}
	assertInboundSeed(t, db)
}

func assertInboundSeed(t *testing.T, db interface {
	Get(dest any, query string, args ...any) error
	Select(dest any, query string, args ...any) error
}) {
	t.Helper()
	type row struct {
		PartCode string  `db:"code"`
		Qty      float64 `db:"qty"`
		BatchNo  string  `db:"batch_no"`
		Note     string  `db:"note"`
	}
	var rows []row
	if err := db.Select(&rows, `
		SELECT p.code AS code, i.qty AS qty, i.batch_no AS batch_no, i.note AS note
		FROM part_inbounds i JOIN parts p ON p.id = i.part_id
		ORDER BY p.code`); err != nil {
		t.Fatalf("select seed rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("seeded %d rows, want 2 (zero-stock part excluded): %+v", len(rows), rows)
	}
	batchNoRe := regexp.MustCompile(`^RK-\d{8}-\d{4,}$`)
	seen := map[string]bool{}
	for _, r := range rows {
		wantQty := 100.0
		if r.PartCode == "LEG-FRAC" {
			wantQty = 12.5
		}
		if r.Qty != wantQty {
			t.Fatalf("%s seeded qty %v, want %v", r.PartCode, r.Qty, wantQty)
		}
		if r.Note != "历史库存初始化" {
			t.Fatalf("%s note = %q", r.PartCode, r.Note)
		}
		if !batchNoRe.MatchString(r.BatchNo) {
			t.Fatalf("%s batch_no %q does not match RK-YYYYMMDD-NNNN", r.PartCode, r.BatchNo)
		}
		if seen[r.BatchNo] {
			t.Fatalf("batch_no %q duplicated", r.BatchNo)
		}
		seen[r.BatchNo] = true
	}
	// 每个零件恰好一笔。
	var per int
	if err := db.Get(&per, `SELECT COUNT(*) FROM part_inbounds WHERE part_id = (SELECT id FROM parts WHERE code='LEG-A')`); err != nil || per != 1 {
		t.Fatalf("LEG-A rows = %d err=%v, want exactly 1", per, err)
	}
}
