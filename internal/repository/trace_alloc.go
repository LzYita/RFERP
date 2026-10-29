package repository

import (
	"database/sql"
	"errors"
	"fmt"

	"app/internal/model"
)

// 投料的批次分配：一条 batch_trace（本批某零件一共用了多少）挂着若干条
// batch_trace_allocs（从哪个入库批次出、出多少、第几顺位）。
//
// 三个约束贯穿本文件：
//  1. 同一 (batch_id, part_id) 只有一个分配方案——记录投料是覆盖不是追加；
//  2. 扣料顺序以 position 为准，分配没覆盖到的部分再走先进先出；
//  3. 占用（未完成批次的预定）只是计划，不改 remaining；完成生产才扣。

// replacePartTrace 在同一事务里把 (batch, part) 的投料记录与分配整体替换。
// 先删分配再删主记录，避免中间态出现"主记录没了分配还引用着"的片刻。
func replacePartTrace(q queryer, trace *model.BatchTrace) (int64, error) {
	if _, err := q.Exec(`DELETE FROM batch_trace_allocs WHERE batch_id = ? AND part_id = ?`,
		trace.BatchID, trace.PartID); err != nil {
		return 0, fmt.Errorf("clear trace allocs: %w", err)
	}
	if _, err := q.Exec(`DELETE FROM batch_trace WHERE batch_id = ? AND part_id = ?`,
		trace.BatchID, trace.PartID); err != nil {
		return 0, fmt.Errorf("clear trace: %w", err)
	}
	res, err := q.Exec(
		`INSERT INTO batch_trace (batch_id,part_id,part_batch_no,used_qty,supplier,operator) VALUES (?,?,?,?,?,?)`,
		trace.BatchID, trace.PartID, trace.PartBatchNo, trace.UsedQty, trace.Supplier, trace.Operator,
	)
	if err != nil {
		return 0, TranslateError(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for i, a := range trace.Allocations {
		if _, err := q.Exec(
			`INSERT INTO batch_trace_allocs (batch_id,part_id,inbound_id,qty,position) VALUES (?,?,?,?,?)`,
			trace.BatchID, trace.PartID, a.InboundID, a.Qty, i); err != nil {
			return 0, fmt.Errorf("insert trace alloc %d: %w", i+1, err)
		}
	}
	return id, nil
}

// batchAllocsByPart 读出某生产批次的全部投料分配，按零件分组。
// 只用于展示：batch_no 取自 part_inbounds，历史行（v18 前）没有分配，返回空。
func batchAllocsByPart(q queryer, batchID int64) (map[int64][]model.BatchTraceAlloc, error) {
	type row struct {
		PartID    int64   `db:"part_id"`
		InboundID int64   `db:"inbound_id"`
		BatchNo   string  `db:"batch_no"`
		Qty       float64 `db:"qty"`
		Position  int     `db:"position"`
	}
	var rows []row
	if err := q.Select(&rows, `
		SELECT a.part_id AS part_id, a.inbound_id AS inbound_id, i.batch_no AS batch_no,
		       a.qty AS qty, a.position AS position
		FROM batch_trace_allocs a
		JOIN part_inbounds i ON i.id = a.inbound_id
		WHERE a.batch_id = ?
		ORDER BY a.part_id, a.position, a.id`, batchID); err != nil {
		return nil, err
	}
	out := make(map[int64][]model.BatchTraceAlloc, len(rows))
	for _, r := range rows {
		out[r.PartID] = append(out[r.PartID], model.BatchTraceAlloc{
			InboundID: r.InboundID,
			BatchNo:   r.BatchNo,
			Qty:       round2(r.Qty),
			Position:  r.Position,
		})
	}
	return out, nil
}

// inboundOccupancy 统计每个入库批次被未完成批次预定了多少。
//
// 已完成（2）的批次消耗已从 remaining 扣掉，再算一次就是重复占用；
// 已撤销（4）不会扣任何东西。口径与投影里的 isOpenBatchStatus 一致。
func inboundOccupancy(q queryer, partID int64) (map[int64][]model.PartInboundAlloc, error) {
	type row struct {
		InboundID int64   `db:"inbound_id"`
		BatchNo   string  `db:"batch_no"`
		Qty       float64 `db:"qty"`
	}
	var rows []row
	if err := q.Select(&rows, `
		SELECT a.inbound_id AS inbound_id, b.batch_no AS batch_no, SUM(a.qty) AS qty
		FROM batch_trace_allocs a
		JOIN product_batches b ON b.id = a.batch_id
		WHERE a.part_id = ? AND b.status IN (0,1,3)
		GROUP BY a.inbound_id, b.batch_no
		ORDER BY a.inbound_id, b.batch_no`, partID); err != nil {
		return nil, err
	}
	out := make(map[int64][]model.PartInboundAlloc, len(rows))
	for _, r := range rows {
		out[r.InboundID] = append(out[r.InboundID], model.PartInboundAlloc{
			BatchNo: r.BatchNo,
			Qty:     round2(r.Qty),
		})
	}
	return out, nil
}

// traceAllocStep 是一条扣料计划：从哪笔入库扣、最多扣多少。
type traceAllocStep struct {
	InboundID int64   `db:"inbound_id"`
	Qty       float64 `db:"qty"`
}

// traceAllocPlan 读出某批次某零件的扣料计划（position 升序）。
// 数量是计划值：完成时先扣到计划量，还没扣够再走先进先出，
// 这样界面上「这批用 6、那批用 4」和实际扣减是同一个数。
func traceAllocPlan(q queryer, batchID, partID int64) ([]traceAllocStep, error) {
	rows := make([]traceAllocStep, 0)
	if err := q.Select(&rows, `
		SELECT inbound_id AS inbound_id, qty AS qty FROM batch_trace_allocs
		WHERE batch_id = ? AND part_id = ?
		ORDER BY position, id`, batchID, partID); err != nil {
		return nil, err
	}
	return rows, nil
}

// ---- 事务内入口 ----

// ReplacePartTrace 见 replacePartTrace。
func (t *Tx) ReplacePartTrace(trace *model.BatchTrace) (int64, error) {
	return replacePartTrace(t.tx, trace)
}

// ReplacePartTrace 见 replacePartTrace。
func (t *SQLiteTx) ReplacePartTrace(trace *model.BatchTrace) (int64, error) {
	return replacePartTrace(t.q, trace)
}

// DrawPartInboundForBatch 按该批次的投料分配顺序扣入库批次；
// 分配没覆盖到的余量再按先进先出扣。完成生产调用它而不是纯 FIFO，
// 用户排的先后顺序才真的生效。
func (t *Tx) DrawPartInboundForBatch(partID, batchID int64, qty float64) error {
	return drawPartInboundForBatch(t.tx, partID, batchID, qty, true)
}

// DrawPartInboundForBatch 见 (*Tx).DrawPartInboundForBatch。
func (t *SQLiteTx) DrawPartInboundForBatch(partID, batchID int64, qty float64) error {
	return drawPartInboundForBatch(t.q, partID, batchID, qty, false)
}

// ---- 读侧入口 ----

// ListBatchTraceAllocs 见 batchAllocsByPart。
func (r *Repository) ListBatchTraceAllocs(batchID int64) (map[int64][]model.BatchTraceAlloc, error) {
	return batchAllocsByPart(r.db, batchID)
}

// ListBatchTraceAllocs 见 batchAllocsByPart。
func (s *SQLiteStore) ListBatchTraceAllocs(batchID int64) (map[int64][]model.BatchTraceAlloc, error) {
	return batchAllocsByPart(s.db, batchID)
}

// InboundOccupancy 见 inboundOccupancy。
func (r *Repository) InboundOccupancy(partID int64) (map[int64][]model.PartInboundAlloc, error) {
	return inboundOccupancy(r.db, partID)
}

// InboundOccupancy 见 inboundOccupancy。
func (s *SQLiteStore) InboundOccupancy(partID int64) (map[int64][]model.PartInboundAlloc, error) {
	return inboundOccupancy(s.db, partID)
}

// deletePartTraces 删掉 (batch, part) 的投料记录与批次分配，返回删掉的记录条数。
// 「零件标记为跳过」时调用：跳过意味着这批不用它，残留的投料记录会同时
// 出现在追溯表里、又不会被完成生产扣减，两者互相矛盾。
func deletePartTraces(q queryer, batchID, partID int64) (int64, error) {
	if _, err := q.Exec(`DELETE FROM batch_trace_allocs WHERE batch_id = ? AND part_id = ?`,
		batchID, partID); err != nil {
		return 0, fmt.Errorf("clear trace allocs: %w", err)
	}
	res, err := q.Exec(`DELETE FROM batch_trace WHERE batch_id = ? AND part_id = ?`, batchID, partID)
	if err != nil {
		return 0, fmt.Errorf("clear trace: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}

// DeletePartTrace 见 deletePartTraces。
func (t *Tx) DeletePartTrace(batchID, partID int64) (int64, error) {
	return deletePartTraces(t.tx, batchID, partID)
}

// DeletePartTrace 见 deletePartTraces。
func (t *SQLiteTx) DeletePartTrace(batchID, partID int64) (int64, error) {
	return deletePartTraces(t.q, batchID, partID)
}

// ---- 服务端校验与占用统计用的读侧 ----

// InboundAllocRow 是一条分配明细的原始行（批次 + 入库批次 + 数量）。
type InboundAllocRow struct {
	BatchID   int64   `db:"batch_id"`
	InboundID int64   `db:"inbound_id"`
	Qty       float64 `db:"qty"`
}

// inboundAllocsOfPart 读某零件的全部分配明细，由服务层按"批次当前是否仍会
// 消耗该零件"过滤后再算占用（BOM/临时零件变更只影响在产批次）。
func inboundAllocsOfPart(q queryer, partID int64) ([]InboundAllocRow, error) {
	rows := make([]InboundAllocRow, 0)
	err := q.Select(&rows, `
		SELECT batch_id, inbound_id, qty FROM batch_trace_allocs WHERE part_id = ?`, partID)
	return rows, err
}

// inboundAllocQty 返回同一入库批次上「本批次已分配」与「其它在产批次已分配」的量，
// 用于保存投料时校验"分配数量不超过该批可用"。
func inboundAllocQty(q queryer, batchID, inboundID int64) (own, others float64, err error) {
	if err := q.Get(&own, `
		SELECT COALESCE(SUM(qty), 0) FROM batch_trace_allocs
		WHERE batch_id = ? AND inbound_id = ?`, batchID, inboundID); err != nil {
		return 0, 0, err
	}
	if err := q.Get(&others, `
		SELECT COALESCE(SUM(a.qty), 0)
		FROM batch_trace_allocs a
		JOIN product_batches b ON b.id = a.batch_id
		WHERE a.inbound_id = ? AND a.batch_id <> ? AND b.status IN (0,1,3)`,
		inboundID, batchID); err != nil {
		return 0, 0, err
	}
	return round2(own), round2(others), nil
}

// batchTraceParts 返回该批次已经有投料记录的零件 id。
func batchTraceParts(q queryer, batchID int64) ([]int64, error) {
	ids := make([]int64, 0)
	err := q.Select(&ids, `SELECT DISTINCT part_id FROM batch_trace WHERE batch_id = ?`, batchID)
	return ids, err
}

// GetInboundForUpdate 读一笔入库并加行锁（MySQL），保存投料时防止并发超分。
func (t *Tx) GetInboundForUpdate(inboundID int64) (*model.PartInbound, error) {
	var in model.PartInbound
	err := t.tx.Get(&in, `
		SELECT id, part_id, batch_no, qty, COALESCE(remaining, 0) AS remaining, note, created_at
		FROM part_inbounds WHERE id = ? FOR UPDATE`, inboundID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &in, nil
}

// GetInboundForUpdate 见 (*Tx).GetInboundForUpdate（SQLite 由写事务串行化）。
func (t *SQLiteTx) GetInboundForUpdate(inboundID int64) (*model.PartInbound, error) {
	return getInbound(t.q, inboundID)
}

// InboundAllocQty 见 inboundAllocQty。
func (t *Tx) InboundAllocQty(batchID, inboundID int64) (float64, float64, error) {
	return inboundAllocQty(t.tx, batchID, inboundID)
}

// InboundAllocQty 见 inboundAllocQty。
func (t *SQLiteTx) InboundAllocQty(batchID, inboundID int64) (float64, float64, error) {
	return inboundAllocQty(t.q, batchID, inboundID)
}

// BatchTraceParts 见 batchTraceParts。
func (t *Tx) BatchTraceParts(batchID int64) ([]int64, error) {
	return batchTraceParts(t.tx, batchID)
}

// BatchTraceParts 见 batchTraceParts。
func (t *SQLiteTx) BatchTraceParts(batchID int64) ([]int64, error) {
	return batchTraceParts(t.q, batchID)
}

// InboundAllocsOfPart 见 inboundAllocsOfPart。
func (r *Repository) InboundAllocsOfPart(partID int64) ([]InboundAllocRow, error) {
	return inboundAllocsOfPart(r.db, partID)
}

// InboundAllocsOfPart 见 inboundAllocsOfPart。
func (s *SQLiteStore) InboundAllocsOfPart(partID int64) ([]InboundAllocRow, error) {
	return inboundAllocsOfPart(s.db, partID)
}
