package repository

import "app/internal/model"

// tracesForPart 按批次汇总该零件的生产投料记录。
//
// 删除零件时第二道要说明白的拒绝：batch_trace.part_id 的外键同样没有级联，
// 数据库只会回 FOREIGN KEY constraint failed —— 用户不知道是追溯记录挡住的，
// 也不知道涉及哪些批次。按批次汇总比逐条列出更贴近他接下来要做的事
// （到批次里核对），记录条数则用来说明影响范围。
func tracesForPart(q queryer, partID int64) ([]model.PartTraceRef, error) {
	list := make([]model.PartTraceRef, 0)
	err := q.Select(&list, `
		SELECT b.batch_no AS batch_no, COUNT(*) AS records
		FROM batch_trace t
		JOIN product_batches b ON b.id = t.batch_id
		WHERE t.part_id = ?
		GROUP BY b.batch_no
		ORDER BY b.batch_no`, partID)
	return list, err
}

// TracesForPart 见 tracesForPart。
func (r *Repository) TracesForPart(partID int64) ([]model.PartTraceRef, error) {
	return tracesForPart(r.db, partID)
}

// TracesForPart 见 tracesForPart。
func (s *SQLiteStore) TracesForPart(partID int64) ([]model.PartTraceRef, error) {
	return tracesForPart(s.db, partID)
}

// TracesForPart 见 tracesForPart。
func (t *Tx) TracesForPart(partID int64) ([]model.PartTraceRef, error) {
	return tracesForPart(t.tx, partID)
}

// TracesForPart 见 tracesForPart。
func (t *SQLiteTx) TracesForPart(partID int64) ([]model.PartTraceRef, error) {
	return tracesForPart(t.q, partID)
}
