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
