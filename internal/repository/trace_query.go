package repository

import (
	"app/internal/model"
	"github.com/jmoiron/sqlx"
)

const traceByProductSQL = `SELECT t.* FROM batch_trace t JOIN product_batches b ON b.id=t.batch_id WHERE b.batch_no=? ORDER BY t.id`
const traceByPartSQL = `SELECT t.* FROM batch_trace t JOIN parts p ON p.id=t.part_id WHERE p.code=? ORDER BY t.id`

func selectTraces(db *sqlx.DB, query, value string) ([]model.BatchTrace, error) {
	rows := make([]model.BatchTrace, 0)
	err := db.Select(&rows, query, value)
	return rows, err
}

func (r *Repository) TraceByProduct(batchNo string) ([]model.BatchTrace, error) {
	return selectTraces(r.db, traceByProductSQL, batchNo)
}
func (r *Repository) TraceByPart(partCode string) ([]model.BatchTrace, error) {
	return selectTraces(r.db, traceByPartSQL, partCode)
}
func (s *SQLiteStore) TraceByProduct(batchNo string) ([]model.BatchTrace, error) {
	return selectTraces(s.db, traceByProductSQL, batchNo)
}
func (s *SQLiteStore) TraceByPart(partCode string) ([]model.BatchTrace, error) {
	return selectTraces(s.db, traceByPartSQL, partCode)
}

// traceByPartLotSQL 按零件入库批次号反查生产批次：一个入库批次可能被多个
// 生产批次用（含分配行），DISTINCT 去重后按生产批次号排。
const traceByPartLotSQL = `SELECT DISTINCT t.* FROM batch_trace t
	JOIN batch_trace_allocs a ON a.batch_id = t.batch_id AND a.part_id = t.part_id
	JOIN part_inbounds i ON i.id = a.inbound_id
	WHERE i.batch_no = ?
	ORDER BY t.id`

// 旧记录（v18 前）没有分配，自由文本 part_batch_no 命中时也算——
// 查询是给用户找历史的，不是给账本对账的。
const traceByPartLotLegacySQL = `SELECT t.* FROM batch_trace t WHERE t.part_batch_no = ? ORDER BY t.id`

func selectTracesByPartLot(db *sqlx.DB, batchNo string) ([]model.BatchTrace, error) {
	rows := make([]model.BatchTrace, 0)
	if err := db.Select(&rows, traceByPartLotSQL, batchNo); err != nil {
		return nil, err
	}
	legacy := make([]model.BatchTrace, 0)
	if err := db.Select(&legacy, traceByPartLotLegacySQL, batchNo); err != nil {
		return nil, err
	}
	seen := make(map[int64]bool, len(rows))
	for _, r := range rows {
		seen[r.ID] = true
	}
	for _, r := range legacy {
		if !seen[r.ID] {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

// TraceByPartLot 见 selectTracesByPartLot。
func (r *Repository) TraceByPartLot(batchNo string) ([]model.BatchTrace, error) {
	return selectTracesByPartLot(r.db, batchNo)
}

// TraceByPartLot 见 selectTracesByPartLot。
func (s *SQLiteStore) TraceByPartLot(batchNo string) ([]model.BatchTrace, error) {
	return selectTracesByPartLot(s.db, batchNo)
}
