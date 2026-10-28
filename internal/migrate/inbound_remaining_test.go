package migrate

import (
	"fmt"
	"testing"

	"github.com/jmoiron/sqlx"
)

// v17 回填：老库上 Σ(批次数量) 通常大于库存总量（历史扣料只减总量），
// 回填必须按先进先出把差额扣掉，让"总量 = 批次剩余之和"重新成立；
// 反过来库存更大时（历史盘点调高过）要补一笔来源可见的盈余批次。
func TestBackfillInboundRemainingSQLite(t *testing.T) {
	db := openSQLiteTest(t)
	if _, err := RunSQLite(db, SQLiteOptions{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	type seed struct {
		code    string
		stock   float64
		batches []float64
	}
	cases := []seed{
		// 历史消耗 5：最早那批先被用掉 → 5 / 10
		{"FIFO", 15, []float64{10, 10}},
		// 库存比入库还多 7：全部保留 + 补一笔盈余批次
		{"SURPLUS", 12, []float64{5}},
		// 全部用完
		{"EMPTY", 0, []float64{3}},
		// 没有批次也没有库存：什么都不该发生
		{"NOTHING", 0, nil},
	}
	ids := map[string]int64{}
	for _, c := range cases {
		res, err := db.Exec(`INSERT INTO parts (code,name,unit,stock_qty,warn_qty,status) VALUES (?,?,'个',?,0,1)`,
			c.code, c.code, c.stock)
		if err != nil {
			t.Fatalf("insert part %s: %v", c.code, err)
		}
		pid, _ := res.LastInsertId()
		ids[c.code] = pid
		for i, qty := range c.batches {
			// 模拟 v16 落下的行：批次号已回填，remaining 还是 NULL。
			if _, err := db.Exec(`INSERT INTO part_inbounds (part_id, qty, note, batch_no) VALUES (?, ?, '历史库存初始化', ?)`,
				pid, qty, fmt.Sprintf("RK-LEGACY-%s-%d", c.code, i)); err != nil {
				t.Fatalf("insert inbound: %v", err)
			}
		}
	}

	if err := backfillInboundRemaining(db, "'RK-' || ? || '-' || printf('%04d', id)"); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	assertRemaining(t, db, ids["FIFO"], []float64{5, 10})
	assertRemaining(t, db, ids["SURPLUS"], []float64{5, 7})
	assertRemaining(t, db, ids["EMPTY"], []float64{0})
	assertRemaining(t, db, ids["NOTHING"], nil)

	// 盈余批次必须带批次号与可读来源，否则详情页会出现一笔没有来处的库存。
	var note string
	if err := db.Get(&note, `SELECT note FROM part_inbounds WHERE part_id=? AND qty=7`, ids["SURPLUS"]); err != nil {
		t.Fatalf("surplus row: %v", err)
	}
	if note != "历史盘点盈余" {
		t.Fatalf("surplus note = %q", note)
	}
	var unnamed int
	if err := db.Get(&unnamed, `SELECT COUNT(*) FROM part_inbounds WHERE batch_no IS NULL`); err != nil {
		t.Fatalf("count unnamed: %v", err)
	}
	if unnamed != 0 {
		t.Fatalf("%d 行回填后没有批次号（盈余批次也必须能指认来源）", unnamed)
	}

	// 幂等：再跑一次不改任何值（只处理 remaining IS NULL 的行）。
	if err := backfillInboundRemaining(db, "'RK-' || ? || '-' || printf('%04d', id)"); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	assertRemaining(t, db, ids["FIFO"], []float64{5, 10})
	assertRemaining(t, db, ids["SURPLUS"], []float64{5, 7})
}

func assertRemaining(t *testing.T, db *sqlx.DB, partID int64, want []float64) {
	t.Helper()
	var got []float64
	if err := db.Select(&got, `SELECT remaining FROM part_inbounds WHERE part_id=? ORDER BY id`, partID); err != nil {
		t.Fatalf("select remaining: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("part %d remaining = %v, want %v", partID, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("part %d remaining = %v, want %v", partID, got, want)
		}
	}
}
