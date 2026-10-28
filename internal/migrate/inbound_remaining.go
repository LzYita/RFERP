package migrate

import (
	"fmt"
	"math"
	"time"

	"github.com/jmoiron/sqlx"
)

// v17 让入库批次能"各自剩多少"，从而支持逐批次盘点。
//
// 模型：parts.stock_qty 仍是总量，但必须等于该零件全部批次 remaining 之和。
// 扣料按先进先出（id 升序）从批次里扣，撤销时按 part_inbound_draws 记录
// 原样还回；逐批次盘点直接改某批的 remaining，并按差额调整总量。
//
// remaining 允许 NULL 只为了迁移可重跑：回填只处理 IS NULL 的行，
// 中途失败再跑一次不会把已经算好的值覆盖掉。查询侧统一 COALESCE(…, 0)。
const partInboundRemainingMySQL = "ADD COLUMN remaining DECIMAL(12,2) NULL COMMENT '批次剩余，与 parts.stock_qty 合计一致'"
const partInboundRemainingSQLite = "ADD COLUMN remaining NUMERIC NULL"

// part_inbound_draws 记录"某个生产批次从哪一笔入库里扣了多少"，
// 撤销批次时据此把数量还回原批次——只知道总数是不够的，
// 还错批次会让盘点出来的批次剩余立刻失真。
const inboundDrawsDDLMySQL = `CREATE TABLE IF NOT EXISTS part_inbound_draws (
	id         BIGINT AUTO_INCREMENT PRIMARY KEY,
	batch_id   BIGINT NOT NULL,
	inbound_id BIGINT NOT NULL,
	qty        DECIMAL(12,2) NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (batch_id) REFERENCES product_batches(id) ON DELETE CASCADE,
	FOREIGN KEY (inbound_id) REFERENCES part_inbounds(id) ON DELETE CASCADE,
	INDEX idx_draw_batch (batch_id),
	CHECK (qty > 0)
) COMMENT '入库批次扣料明细-撤销回补依据'`

const inboundDrawsDDLite = `CREATE TABLE IF NOT EXISTS part_inbound_draws (
	id         INTEGER PRIMARY KEY,
	batch_id   INTEGER NOT NULL REFERENCES product_batches(id) ON DELETE CASCADE,
	inbound_id INTEGER NOT NULL REFERENCES part_inbounds(id) ON DELETE CASCADE,
	qty        NUMERIC NOT NULL CHECK (qty > 0),
	created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
)`

func applyPartInboundRemainingMySQL(db *sqlx.DB) error {
	ok, err := columnExists(db, "part_inbounds", "remaining")
	if err != nil {
		return err
	}
	if !ok {
		if _, err := db.Exec("ALTER TABLE part_inbounds " + partInboundRemainingMySQL); err != nil {
			return err
		}
	}
	if _, err := db.Exec(inboundDrawsDDLMySQL); err != nil {
		return err
	}
	return backfillInboundRemaining(db, "CONCAT('RK-', ?, '-', LPAD(id, 4, '0'))")
}

func applyPartInboundRemainingSQLite(db *sqlx.DB) error {
	rows, err := db.Queryx(`PRAGMA table_info(part_inbounds)`)
	if err != nil {
		return err
	}
	have := false
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dft any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dft, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "remaining" {
			have = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if !have {
		if _, err := db.Exec("ALTER TABLE part_inbounds " + partInboundRemainingSQLite); err != nil {
			return err
		}
	}
	if _, err := db.Exec(inboundDrawsDDLite); err != nil {
		return err
	}
	return backfillInboundRemaining(db, "'RK-' || ? || '-' || printf('%04d', id)")
}

// backfillInboundRemaining 把现有库对齐到"总量 = 批次剩余之和"。
//
// v16 之前扣料只减总量、不记批次，所以老库上 Σ(qty) 往往大于 stock_qty：
// 差额就是历史消耗，按先进先出从未回填的批次里扣掉（最早入的先用完，
// 与今后的扣料口径一致）。反过来 stock_qty 更大时（历史盘点把数量调高过），
// 补一笔"历史盘点盈余"入库批次，避免差额凭空消失。
//
// 只处理 remaining IS NULL 的行；已经回填过的行原样保留（迁移失败重跑安全）。
func backfillInboundRemaining(db *sqlx.DB, batchNoExpr string) error {
	type partRow struct {
		ID    int64   `db:"id"`
		Stock float64 `db:"stock_qty"`
	}
	var parts []partRow
	if err := db.Select(&parts, `SELECT id, stock_qty FROM parts`); err != nil {
		return err
	}
	day := time.Now().Format("20060102")
	for _, p := range parts {
		type batchRow struct {
			ID  int64   `db:"id"`
			Qty float64 `db:"qty"`
		}
		var batches []batchRow
		if err := db.Select(&batches, `
			SELECT id, qty FROM part_inbounds
			WHERE part_id = ? AND remaining IS NULL
			ORDER BY id`, p.ID); err != nil {
			return err
		}
		if len(batches) == 0 {
			continue
		}
		var total float64
		for _, b := range batches {
			total += b.Qty
		}
		// 该零件已回填过的剩余（重跑续做时不为 0）。
		var done float64
		if err := db.Get(&done, `
			SELECT COALESCE(SUM(remaining), 0) FROM part_inbounds
			WHERE part_id = ? AND remaining IS NOT NULL`, p.ID); err != nil {
			return err
		}
		stock := round2(p.Stock - done) // 留给这批未回填批次的数量
		if stock < 0 {
			stock = 0
		}
		if stock >= total {
			for _, b := range batches {
				if _, err := db.Exec(`UPDATE part_inbounds SET remaining = ? WHERE id = ?`, round2(b.Qty), b.ID); err != nil {
					return err
				}
			}
			if surplus := round2(stock - total); surplus > 0 {
				if err := insertSurplusInbound(db, p.ID, surplus, day, batchNoExpr); err != nil {
					return err
				}
			}
			continue
		}
		// 先进先出地扣：最早入的批次先被用掉。
		drain := round2(total - stock)
		for _, b := range batches {
			left := b.Qty
			if drain > 0 {
				take := drain
				if take > left {
					take = left
				}
				left = round2(left - take)
				drain = round2(drain - take)
			}
			if _, err := db.Exec(`UPDATE part_inbounds SET remaining = ? WHERE id = ?`, round2(left), b.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// insertSurplusInbound 给"历史盘点把库存调高过"的零件补一笔批次，
// 让总量与批次剩余重新对得上。批次号沿用同一规则，先插行后回填。
func insertSurplusInbound(db *sqlx.DB, partID int64, qty float64, day, batchNoExpr string) error {
	res, err := db.Exec(`INSERT INTO part_inbounds (part_id, qty, remaining, note) VALUES (?, ?, ?, '历史盘点盈余')`,
		partID, qty, qty)
	if err != nil {
		return fmt.Errorf("insert surplus inbound: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	if _, err := db.Exec(`UPDATE part_inbounds SET batch_no = `+batchNoExpr+` WHERE id = ?`, day, id); err != nil {
		return fmt.Errorf("set surplus batch_no: %w", err)
	}
	return nil
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}
