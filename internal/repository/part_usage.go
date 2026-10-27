package repository

import "app/internal/model"

// batchUsesForPart 列出"零件仍被批次使用"的来源：临时零件、跳过零件、
// 完成扣料。三张表都有 parts 外键且不级联，直接删只会得到
// FOREIGN KEY constraint failed；删除守卫要在报错前说清是哪一批、哪一类。
func batchUsesForPart(q queryer, partID int64) ([]model.PartBatchUse, error) {
	list := make([]model.PartBatchUse, 0)
	err := q.Select(&list, `
		SELECT b.batch_no AS batch_no, 'temp' AS reason
		FROM batch_temp_parts t
		JOIN product_batches b ON b.id = t.batch_id
		WHERE t.part_id = ?
		UNION ALL
		SELECT b.batch_no AS batch_no, 'skip' AS reason
		FROM batch_skip_parts s
		JOIN product_batches b ON b.id = s.batch_id
		WHERE s.part_id = ?
		UNION ALL
		SELECT b.batch_no AS batch_no, 'consumed' AS reason
		FROM batch_consumptions c
		JOIN product_batches b ON b.id = c.batch_id
		WHERE c.part_id = ?
		ORDER BY batch_no, reason`, partID, partID, partID)
	return list, err
}

// BatchUsesForPart 见 batchUsesForPart。
func (t *Tx) BatchUsesForPart(partID int64) ([]model.PartBatchUse, error) {
	return batchUsesForPart(t.tx, partID)
}

// BatchUsesForPart 见 batchUsesForPart。
func (t *SQLiteTx) BatchUsesForPart(partID int64) ([]model.PartBatchUse, error) {
	return batchUsesForPart(t.q, partID)
}
