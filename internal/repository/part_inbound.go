package repository

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"app/internal/model"
)

// round2 与 service 的 quantizeQuantity 同口径（两位小数），
// 避免批次剩余累出 0.30000000000000004 这种尾巴。
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// addPartInbound 记一笔入库并生成批次号。
//
// 批次号 RK-YYYYMMDD-NNNN 里的 NNNN 是行 id：id 由自增分配、全局唯一，
// 生成天然无竞态。id 要等插入后才知道，所以先插行（batch_no 为 NULL）
// 再回填——两步都在调用方的 StockIn 事务里，外部读者永远看不到 NULL。
//
// remaining 与 qty 一起写入：刚入库的批次剩余就是全量。
func addPartInbound(q queryer, partID int64, qty float64, note *string) (string, error) {
	res, err := q.Exec(
		`INSERT INTO part_inbounds (part_id, qty, remaining, note) VALUES (?, ?, ?, ?)`,
		partID, qty, qty, note,
	)
	if err != nil {
		return "", fmt.Errorf("insert part inbound: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return "", fmt.Errorf("part inbound last insert id: %w", err)
	}
	batchNo := fmt.Sprintf("RK-%s-%04d", time.Now().Format("20060102"), id)
	if _, err := q.Exec(`UPDATE part_inbounds SET batch_no = ? WHERE id = ?`, batchNo, id); err != nil {
		return "", fmt.Errorf("set part inbound batch_no: %w", err)
	}
	return batchNo, nil
}

// listPartInbounds 按时间倒序列出零件的入库批次：最新一笔摆在最上面，
// 与"刚入库的批次最值得核对"的阅读顺序一致。
//
// remaining 用 COALESCE：v17 迁移前插入的行在回填前是 NULL，读侧按 0 处理，
// 避免一个未回填的库让整页崩掉。
func listPartInbounds(q queryer, partID int64) ([]model.PartInbound, error) {
	list := make([]model.PartInbound, 0)
	err := q.Select(&list, `
		SELECT id, part_id, batch_no, qty, COALESCE(remaining, 0) AS remaining, note, created_at
		FROM part_inbounds
		WHERE part_id = ?
		ORDER BY id DESC`, partID)
	return list, err
}

// partInboundsForUpdate 按先进先出（id 升序）读零件全部批次并加行锁，
// 供扣料与盘点在同一事务里改写剩余。
func partInboundsForUpdate(q queryer, partID int64, forUpdate bool) ([]model.PartInbound, error) {
	list := make([]model.PartInbound, 0)
	suffix := ""
	if forUpdate {
		suffix = " FOR UPDATE"
	}
	err := q.Select(&list, `
		SELECT id, part_id, batch_no, qty, COALESCE(remaining, 0) AS remaining, note, created_at
		FROM part_inbounds
		WHERE part_id = ?
		ORDER BY id`+suffix, partID)
	return list, err
}

// drawPartInboundFIFO 从一个零件的批次里按先进先出扣掉 qty，并逐笔记录
// 扣料明细（撤销时要据此还回原批次）。
//
// 库里其余批次剩余之和必然等于 parts.stock_qty（服务层维护的不变量），
// 所以正常路径不会扣不够；扣不够说明账本已被破坏，宁可报错也不要把
// 负荷摊到别的零件或把总量扣成负数。
// forUpdate 只在 MySQL 上为真：SQLite 用 IMMEDIATE 事务串行化写入，
// 不接受 FOR UPDATE 语法（照抄会得到 "near FOR: syntax error"）。
func drawPartInboundFIFO(q queryer, partID, batchID int64, qty float64, forUpdate bool) error {
	return deductPartInboundFIFO(q, partID, qty, &batchID, forUpdate)
}

// reducePartInboundFIFO 只扣批次剩余、不记扣料明细：盘点调减属于"货确实
// 少了"，不归属于任何一个生产批次，将来撤销批次也不该把它加回来。
func reducePartInboundFIFO(q queryer, partID int64, qty float64, forUpdate bool) error {
	return deductPartInboundFIFO(q, partID, qty, nil, forUpdate)
}

func deductPartInboundFIFO(q queryer, partID int64, qty float64, batchID *int64, forUpdate bool) error {
	if qty <= 0 {
		return nil
	}
	list, err := partInboundsForUpdate(q, partID, forUpdate)
	if err != nil {
		return fmt.Errorf("load inbounds for draw: %w", err)
	}
	left := qty
	for _, in := range list {
		if left <= 0 {
			break
		}
		if in.Remaining <= 0 {
			continue
		}
		take := in.Remaining
		if take > left {
			take = left
		}
		if _, err := q.Exec(`UPDATE part_inbounds SET remaining = ? WHERE id = ?`, round2(in.Remaining-take), in.ID); err != nil {
			return fmt.Errorf("update inbound remaining: %w", err)
		}
		if batchID != nil {
			if _, err := q.Exec(
				`INSERT INTO part_inbound_draws (batch_id, inbound_id, qty) VALUES (?, ?, ?)`,
				*batchID, in.ID, take); err != nil {
				return fmt.Errorf("record inbound draw: %w", err)
			}
		}
		left = round2(left - take)
	}
	if left > 0 {
		return fmt.Errorf("批次剩余不足以扣减：还差 %.2f（账本与库存总量不一致，请先盘点）", left)
	}
	return nil
}

// restorePartInboundDraws 撤销批次时把该批次扣过的数量按原批次还回。
// 返回还回的总量，供调用方核对库存增量。
func restorePartInboundDraws(q queryer, batchID int64) (float64, error) {
	type row struct {
		InboundID int64   `db:"inbound_id"`
		Qty       float64 `db:"qty"`
	}
	var rows []row
	if err := q.Select(&rows, `
		SELECT inbound_id, SUM(qty) AS qty
		FROM part_inbound_draws
		WHERE batch_id = ?
		GROUP BY inbound_id`, batchID); err != nil {
		return 0, fmt.Errorf("load inbound draws: %w", err)
	}
	var total float64
	for _, r := range rows {
		if _, err := q.Exec(`UPDATE part_inbounds SET remaining = COALESCE(remaining, 0) + ? WHERE id = ?`, r.Qty, r.InboundID); err != nil {
			return 0, fmt.Errorf("restore inbound remaining: %w", err)
		}
		total = round2(total + r.Qty)
	}
	if _, err := q.Exec(`DELETE FROM part_inbound_draws WHERE batch_id = ?`, batchID); err != nil {
		return 0, fmt.Errorf("clear inbound draws: %w", err)
	}
	return total, nil
}

// batchInboundDraws 返回某生产批次在指定零件上的扣料总量（撤销前核对用）。
func batchInboundDraws(q queryer, batchID, partID int64) (float64, error) {
	var total float64
	err := q.Get(&total, `
		SELECT COALESCE(SUM(d.qty), 0)
		FROM part_inbound_draws d
		JOIN part_inbounds i ON i.id = d.inbound_id
		WHERE d.batch_id = ? AND i.part_id = ?`, batchID, partID)
	return total, err
}

// setInboundRemaining 把某批次的剩余设为指定值（逐批次盘点用）。
func setInboundRemaining(q queryer, inboundID int64, remaining float64) error {
	_, err := q.Exec(`UPDATE part_inbounds SET remaining = ? WHERE id = ?`, remaining, inboundID)
	return err
}

// getInbound 读一笔入库（盘点前核对归属与旧值）。
func getInbound(q queryer, inboundID int64) (*model.PartInbound, error) {
	var in model.PartInbound
	err := q.Get(&in, `
		SELECT id, part_id, batch_no, qty, COALESCE(remaining, 0) AS remaining, note, created_at
		FROM part_inbounds WHERE id = ?`, inboundID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &in, nil
}

// AddPartInbound 见 addPartInbound。
func (t *Tx) AddPartInbound(partID int64, qty float64, note *string) (string, error) {
	return addPartInbound(t.tx, partID, qty, note)
}

// ListPartInbounds 见 listPartInbounds。
func (t *Tx) ListPartInbounds(partID int64) ([]model.PartInbound, error) {
	return listPartInbounds(t.tx, partID)
}

// AddPartInbound 见 addPartInbound。
func (t *SQLiteTx) AddPartInbound(partID int64, qty float64, note *string) (string, error) {
	return addPartInbound(t.q, partID, qty, note)
}

// ListPartInbounds 见 listPartInbounds。
func (t *SQLiteTx) ListPartInbounds(partID int64) ([]model.PartInbound, error) {
	return listPartInbounds(t.q, partID)
}

// ListPartInbounds 见 listPartInbounds。
func (r *Repository) ListPartInbounds(partID int64) ([]model.PartInbound, error) {
	return listPartInbounds(r.db, partID)
}

// ListPartInbounds 见 listPartInbounds。
func (s *SQLiteStore) ListPartInbounds(partID int64) ([]model.PartInbound, error) {
	return listPartInbounds(s.db, partID)
}

// 事务内的批次读写：MySQL 取行锁，SQLite 由调用方的 IMMEDIATE 事务保证互斥。
func (t *Tx) GetInbound(inboundID int64) (*model.PartInbound, error) {
	return getInbound(t.tx, inboundID)
}

func (t *SQLiteTx) GetInbound(inboundID int64) (*model.PartInbound, error) {
	return getInbound(t.q, inboundID)
}

func (t *Tx) DrawPartInboundFIFO(partID, batchID int64, qty float64) error {
	return drawPartInboundFIFO(t.tx, partID, batchID, qty, true)
}

func (t *SQLiteTx) DrawPartInboundFIFO(partID, batchID int64, qty float64) error {
	return drawPartInboundFIFO(t.q, partID, batchID, qty, false)
}

func (t *Tx) ReducePartInboundFIFO(partID int64, qty float64) error {
	return reducePartInboundFIFO(t.tx, partID, qty, true)
}

func (t *SQLiteTx) ReducePartInboundFIFO(partID int64, qty float64) error {
	return reducePartInboundFIFO(t.q, partID, qty, false)
}

func (t *Tx) RestorePartInboundDraws(batchID int64) (float64, error) {
	return restorePartInboundDraws(t.tx, batchID)
}

func (t *SQLiteTx) RestorePartInboundDraws(batchID int64) (float64, error) {
	return restorePartInboundDraws(t.q, batchID)
}

func (t *Tx) BatchInboundDraws(batchID, partID int64) (float64, error) {
	return batchInboundDraws(t.tx, batchID, partID)
}

func (t *SQLiteTx) BatchInboundDraws(batchID, partID int64) (float64, error) {
	return batchInboundDraws(t.q, batchID, partID)
}

func (t *Tx) SetInboundRemaining(inboundID int64, remaining float64) error {
	return setInboundRemaining(t.tx, inboundID, remaining)
}

func (t *SQLiteTx) SetInboundRemaining(inboundID int64, remaining float64) error {
	return setInboundRemaining(t.q, inboundID, remaining)
}
