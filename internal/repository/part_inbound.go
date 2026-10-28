package repository

import (
	"fmt"
	"time"

	"app/internal/model"
)

// addPartInbound 记一笔入库并生成批次号。
//
// 批次号 RK-YYYYMMDD-NNNN 里的 NNNN 是行 id：id 由自增分配、全局唯一，
// 生成天然无竞态。id 要等插入后才知道，所以先插行（batch_no 为 NULL）
// 再回填——两步都在调用方的 StockIn 事务里，外部读者永远看不到 NULL。
func addPartInbound(q queryer, partID int64, qty float64, note *string) (string, error) {
	res, err := q.Exec(
		`INSERT INTO part_inbounds (part_id, qty, note) VALUES (?, ?, ?)`,
		partID, qty, note,
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
func listPartInbounds(q queryer, partID int64) ([]model.PartInbound, error) {
	list := make([]model.PartInbound, 0)
	err := q.Select(&list, `
		SELECT id, part_id, batch_no, qty, note, created_at
		FROM part_inbounds
		WHERE part_id = ?
		ORDER BY id DESC`, partID)
	return list, err
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
