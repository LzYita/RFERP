package repository

import (
	"app/internal/model"
	"github.com/jmoiron/sqlx"
)

// 三个反查各有"完全匹配"与"部分匹配"两条 SQL。
//
// 部分匹配刻意用 instr(lower(列), lower(?)) 而不是 LIKE：
//   - LIKE 的通配符转义在两种引擎里不一致（MySQL 默认把 \ 当转义符，
//     SQLite 不认），用户输入里的 % / _ 会变成通配符；
//   - instr 两边都有，参数化后行为一致，也不用拼字符串。
//
// 每条都带 LIMIT 500：部分匹配在短片段上可能命中全表，不能把整库记录
// 一次甩给界面（渲染与传输都会卡）。前端 traceQuery.tsx 的 RESULT_LIMIT
// 与这个数字必须一致——达到上限时由界面提示"只显示前 500 条"。
//
// 零件查询同时匹配编码与名称：界面上的入口本来就叫「零件编码/名称」，
// 只认编码会让按名称查询在前端兜一圈（旧实现逐批次拉全表再过滤）。
const (
	traceByProductSQL         = `SELECT t.* FROM batch_trace t JOIN product_batches b ON b.id=t.batch_id WHERE b.batch_no=? ORDER BY t.id LIMIT 500`
	traceByProductContainsSQL = `SELECT t.* FROM batch_trace t JOIN product_batches b ON b.id=t.batch_id WHERE instr(lower(b.batch_no), lower(?)) > 0 ORDER BY t.id LIMIT 500`
	traceByPartSQL            = `SELECT t.* FROM batch_trace t JOIN parts p ON p.id=t.part_id WHERE p.code=? OR p.name=? ORDER BY t.id LIMIT 500`
	traceByPartContainsSQL    = `SELECT t.* FROM batch_trace t JOIN parts p ON p.id=t.part_id WHERE instr(lower(p.code), lower(?)) > 0 OR instr(lower(p.name), lower(?)) > 0 ORDER BY t.id LIMIT 500`
)

func selectTraces(db *sqlx.DB, query string, args ...any) ([]model.BatchTrace, error) {
	rows := make([]model.BatchTrace, 0)
	err := db.Select(&rows, query, args...)
	return rows, err
}

func (r *Repository) TraceByProduct(batchNo string, partial bool) ([]model.BatchTrace, error) {
	if partial {
		return selectTraces(r.db, traceByProductContainsSQL, batchNo)
	}
	return selectTraces(r.db, traceByProductSQL, batchNo)
}

func (r *Repository) TraceByPart(value string, partial bool) ([]model.BatchTrace, error) {
	if partial {
		return selectTraces(r.db, traceByPartContainsSQL, value, value)
	}
	return selectTraces(r.db, traceByPartSQL, value, value)
}

func (s *SQLiteStore) TraceByProduct(batchNo string, partial bool) ([]model.BatchTrace, error) {
	if partial {
		return selectTraces(s.db, traceByProductContainsSQL, batchNo)
	}
	return selectTraces(s.db, traceByProductSQL, batchNo)
}

func (s *SQLiteStore) TraceByPart(value string, partial bool) ([]model.BatchTrace, error) {
	if partial {
		return selectTraces(s.db, traceByPartContainsSQL, value, value)
	}
	return selectTraces(s.db, traceByPartSQL, value, value)
}

// traceByPartLotSQL 按零件入库批次号反查生产批次：一个入库批次可能被多个
// 生产批次用（含分配行），DISTINCT 去重后按生产批次号排。
const traceByPartLotSQL = `SELECT DISTINCT t.* FROM batch_trace t
	JOIN batch_trace_allocs a ON a.batch_id = t.batch_id AND a.part_id = t.part_id
	JOIN part_inbounds i ON i.id = a.inbound_id
	WHERE i.batch_no = ?
	ORDER BY t.id LIMIT 500`

const traceByPartLotContainsSQL = `SELECT DISTINCT t.* FROM batch_trace t
	JOIN batch_trace_allocs a ON a.batch_id = t.batch_id AND a.part_id = t.part_id
	JOIN part_inbounds i ON i.id = a.inbound_id
	WHERE instr(lower(i.batch_no), lower(?)) > 0
	ORDER BY t.id LIMIT 500`

// 旧记录（v18 前）没有分配，自由文本 part_batch_no 命中时也算——
// 查询是给用户找历史的，不是给账本对账的。
const traceByPartLotLegacySQL = `SELECT t.* FROM batch_trace t WHERE t.part_batch_no = ? ORDER BY t.id LIMIT 500`

const traceByPartLotLegacyContainsSQL = `SELECT t.* FROM batch_trace t WHERE instr(lower(t.part_batch_no), lower(?)) > 0 ORDER BY t.id LIMIT 500`

func selectTracesByPartLot(db *sqlx.DB, batchNo string, partial bool) ([]model.BatchTrace, error) {
	allocSQL, legacySQL := traceByPartLotSQL, traceByPartLotLegacySQL
	if partial {
		allocSQL, legacySQL = traceByPartLotContainsSQL, traceByPartLotLegacyContainsSQL
	}
	rows := make([]model.BatchTrace, 0)
	if err := db.Select(&rows, allocSQL, batchNo); err != nil {
		return nil, err
	}
	legacy := make([]model.BatchTrace, 0)
	if err := db.Select(&legacy, legacySQL, batchNo); err != nil {
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
func (r *Repository) TraceByPartLot(batchNo string, partial bool) ([]model.BatchTrace, error) {
	return selectTracesByPartLot(r.db, batchNo, partial)
}

// TraceByPartLot 见 selectTracesByPartLot。
func (s *SQLiteStore) TraceByPartLot(batchNo string, partial bool) ([]model.BatchTrace, error) {
	return selectTracesByPartLot(s.db, batchNo, partial)
}
