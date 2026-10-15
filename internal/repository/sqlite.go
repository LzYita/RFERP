package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"app/internal/model"

	_ "modernc.org/sqlite"
)

// SQLiteStore is the Local single-file Store (D-013 / Phase E).
// Write paths use BEGIN IMMEDIATE on a dedicated connection so lock upgrade
// cannot fail mid-transaction. "ForUpdate" reads are plain SELECTs inside that
// write transaction (SQLite is single-writer at the DB level).
type SQLiteStore struct {
	db *sqlx.DB
}

// SQLiteTx is the SQLite transaction handle implementing TxOps.
// q is the queryer bound to the dedicated *sqlx.Conn that has BEGIN IMMEDIATE open.
type SQLiteTx struct {
	q queryer
}

// SQLite 主码（extended code 的低 8 位），见 SQLite 的 result code 表。
const (
	sqliteBusy   = 5  // SQLITE_BUSY
	sqliteLocked = 6  // SQLITE_LOCKED
	sqliteIO     = 10 // SQLITE_IOERR（含 READ/SHMOPEN 等扩展码）
)

// 开库重试：次数与退避。合计约 6 秒。
//
// 为什么需要：软件更新是"**先启动新进程、再由老进程退出**"
// （internal/update.ApplyAndRestart），新进程只等单实例锁——而单实例锁比文件句柄
// 先释放，于是新进程会在老进程还在拆 WAL/SHM 句柄的时候去开库，驱动报
// `disk I/O error (1546)`（主码 10），用户看到的是"RFERP 无法启动"，再手动点一次
// 就好了。2026-10-15 的实机演练复现过，见 docs/plans/wails-migration.md。
//
// DSN 里的 busy_timeout(5000) 救不了这个：它只处理 BUSY/LOCKED，不处理硬 I/O 错误。
//
// 只能在新版本这边修：1.3.0 已经发布，它的 ApplyAndRestart 改不了了。
var sqliteOpenAttempts = []time.Duration{0, 200 * time.Millisecond, 400 * time.Millisecond,
	800 * time.Millisecond, 1600 * time.Millisecond, 3 * time.Second}

// openSQLiteOnce 是真正开一次库的那一步，单独抽出来是为了测试能替换它：
// 触发一次真实的 IOERR 需要两个进程在正确的时刻抢同一个文件，测试里做不到。
var openSQLiteOnce = func(path string) (*sqlx.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: serializes writers and keeps session PRAGMAs predictable.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// transientSQLiteError 判断一个开库错误是否值得重试。
//
// 用接口而不是具体类型：驱动是 modernc.org/sqlite（纯 Go），它的 *Error 带 Code() int；
// 万一换驱动（例如 mattn/go-sqlite3），只要也提供 Code() int 就能继续工作。
//
// 只重试 BUSY / LOCKED / IO 三类：
//   - BUSY、LOCKED 是并发写/锁竞争，busy_timeout 有时也会漏出来；
//   - IO 多半是"别的进程正在拆这个文件"或杀软/索引器正在扫，属瞬时；
//   - **不重试 CANTOPEN(14)**：路径错、目录不存在、没权限这些是配置问题，重试只会
//     把一个明确的报错拖成几秒后才报出来。
func transientSQLiteError(err error) bool {
	var coder interface{ Code() int }
	if !errors.As(err, &coder) {
		return false
	}
	switch coder.Code() & 0xFF {
	case sqliteBusy, sqliteLocked, sqliteIO:
		return true
	}
	return false
}

// OpenSQLite opens (creating if needed) a SQLite database with product PRAGMAs.
//
// 瞬时错误（见 transientSQLiteError）会按 sqliteOpenAttempts 的退避重试，
// 每次重试都写一行日志——不写的话用户只会看到"启动失败"，不知道程序等了几秒。
func OpenSQLite(path string) (*sqlx.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	var lastErr error
	for attempt, wait := range sqliteOpenAttempts {
		if attempt > 0 {
			log.Printf("sqlite open retry %d/%d after %s: %v", attempt, len(sqliteOpenAttempts)-1,
				wait, lastErr)
			time.Sleep(wait)
		}
		db, err := openSQLiteOnce(path)
		if err == nil {
			return db, nil
		}
		lastErr = err
		if !transientSQLiteError(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

// NewSQLite wraps an opened SQLite *sqlx.DB (tests may pass temp files).
func NewSQLite(db *sqlx.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func (s *SQLiteStore) WithTx(fn func(TxOps) error) error {
	return s.withImmediateTx(fn)
}

// WithBulkLoad defers foreign keys for the duration of the operation (SQLite
// analogue of MySQL FOREIGN_KEY_CHECKS).
func (s *SQLiteStore) WithBulkLoad(fn func(TxOps) error) error {
	conn, err := s.db.Connx(context.Background())
	if err != nil {
		return err
	}
	if _, err := conn.ExecContext(context.Background(), `PRAGMA defer_foreign_keys = ON`); err != nil {
		_ = conn.Close()
		return fmt.Errorf("defer foreign keys: %w", err)
	}
	err = withImmediateConn(conn, fn)
	_, cleanupErr := conn.ExecContext(context.Background(), `PRAGMA defer_foreign_keys = OFF`)
	var outcome *BulkLoadError
	if cleanupErr != nil || (errors.As(err, &outcome) && outcome.Outcome == BulkLoadUnknown) {
		// An uncertain transaction or PRAGMA state must not be pooled.
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	closeErr := conn.Close()
	if err == nil && (cleanupErr != nil || closeErr != nil) {
		return &BulkLoadError{Outcome: BulkLoadApplied, Err: errors.Join(cleanupErr, closeErr)}
	}
	return errors.Join(err, cleanupErr, closeErr)
}

func (s *SQLiteStore) withImmediateTx(fn func(TxOps) error) error {
	conn, err := s.db.Connx(context.Background())
	if err != nil {
		return err
	}
	err = withImmediateConn(conn, fn)
	var outcome *BulkLoadError
	if errors.As(err, &outcome) && outcome.Outcome == BulkLoadUnknown {
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return errors.Join(err, conn.Close())
}

func withImmediateConn(conn *sqlx.Conn, fn func(TxOps) error) error {
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin immediate: %w", err)
	}
	tx := &SQLiteTx{q: connQueryer{conn}}
	if err := fn(tx); err != nil {
		if _, rollbackErr := conn.ExecContext(ctx, `ROLLBACK`); rollbackErr != nil {
			return &BulkLoadError{Outcome: BulkLoadUnknown, Err: errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))}
		}
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		if _, rollbackErr := conn.ExecContext(ctx, `ROLLBACK`); rollbackErr != nil {
			return &BulkLoadError{Outcome: BulkLoadUnknown, Err: errors.Join(fmt.Errorf("commit transaction: %w", err), fmt.Errorf("rollback transaction: %w", rollbackErr))}
		}
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (t *SQLiteTx) Exec(query string, args ...any) (sql.Result, error) {
	return t.q.Exec(query, args...)
}

// CountRows 读取指定表的行数。table 必须来自代码内固定清单
// （internal/service 的 businessTables），不接受外部输入。
func (t *SQLiteTx) CountRows(table string, dest *int) error {
	return t.q.Get(dest, "SELECT COUNT(*) FROM "+table)
}

func (t *SQLiteTx) GetProduct(id int64) (*model.Product, error) {
	return getProduct(t.q, id)
}

func (t *SQLiteTx) GetProductForUpdate(id int64) (*model.Product, error) {
	return getProduct(t.q, id)
}

func (t *SQLiteTx) CreateProduct(p *model.Product) (int64, error) {
	res, err := t.q.Exec(
		`INSERT INTO products (code,name,spec,unit,status,operator) VALUES (?,?,?,?,?,?)`,
		p.Code, p.Name, p.Spec, p.Unit, p.Status, p.Operator,
	)
	if err != nil {
		return 0, TranslateError(err)
	}
	return res.LastInsertId()
}

func (t *SQLiteTx) UpdateProduct(p *model.Product) (int64, error) {
	res, err := t.q.Exec(
		`UPDATE products SET code=?,name=?,spec=?,unit=?,status=?,operator=?, version=version+1, updated_at=(strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id=? AND version=?`,
		p.Code, p.Name, p.Spec, p.Unit, p.Status, p.Operator, p.ID, p.Version,
	)
	if err != nil {
		return 0, TranslateError(err)
	}
	return res.RowsAffected()
}

func (t *SQLiteTx) DeleteProduct(id int64) error {
	_, err := t.q.Exec(`DELETE FROM products WHERE id=?`, id)
	return err
}

func (t *SQLiteTx) CreatePart(p *model.Part) (int64, error) {
	res, err := t.q.Exec(
		`INSERT INTO parts (code,name,spec,unit,part_type,stock_qty,warn_qty,status,operator,supplier) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		p.Code, p.Name, p.Spec, p.Unit, p.PartType, p.StockQty, p.WarnQty, p.Status, p.Operator, p.Supplier,
	)
	if err != nil {
		return 0, TranslateError(err)
	}
	return res.LastInsertId()
}

func (t *SQLiteTx) UpdatePart(p *model.Part) (int64, error) {
	res, err := t.q.Exec(
		`UPDATE parts SET code=?,name=?,spec=?,unit=?,part_type=?,stock_qty=?,warn_qty=?,status=?,operator=?,supplier=?, version=version+1, updated_at=(strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id=? AND version=?`,
		p.Code, p.Name, p.Spec, p.Unit, p.PartType, p.StockQty, p.WarnQty, p.Status, p.Operator, p.Supplier, p.ID, p.Version,
	)
	if err != nil {
		return 0, TranslateError(err)
	}
	return res.RowsAffected()
}

func (t *SQLiteTx) DeletePart(id int64) error {
	_, err := t.q.Exec(`DELETE FROM parts WHERE id=?`, id)
	return err
}

func (t *SQLiteTx) GetPartForUpdate(id int64) (*model.Part, error) {
	var p model.Part
	err := t.q.Get(&p, `SELECT * FROM parts WHERE id=?`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (t *SQLiteTx) UpdatePartStock(partID int64, newQty float64) error {
	_, err := t.q.Exec(`UPDATE parts SET stock_qty=?, version=version+1, updated_at=(strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id=?`, newQty, partID)
	return err
}

func (t *SQLiteTx) GetBOMByProduct(productID int64) ([]model.BOMItem, error) {
	return getBOM(t.q, productID)
}

func (t *SQLiteTx) CreateBOMItem(b *model.BOMItem) (int64, error) {
	res, err := t.q.Exec(
		`INSERT INTO bom_items (product_id,part_id,quantity,loss_rate,remark,operator,replaceable,use_mode) VALUES (?,?,?,?,?,?,?,?)`,
		b.ProductID, b.PartID, b.Quantity, b.LossRate, b.Remark, b.Operator, b.Replaceable, b.UseMode,
	)
	if err != nil {
		return 0, TranslateError(err)
	}
	return res.LastInsertId()
}

func (t *SQLiteTx) DeleteBOMItem(id int64) error {
	_, err := t.q.Exec(`DELETE FROM bom_items WHERE id=?`, id)
	return err
}

func (t *SQLiteTx) GetBatchForUpdate(id int64) (*model.ProductBatch, error) {
	var b model.ProductBatch
	err := t.q.Get(&b, `SELECT * FROM product_batches WHERE id=?`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (t *SQLiteTx) CreateBatch(b *model.ProductBatch) (int64, error) {
	res, err := t.q.Exec(
		`INSERT INTO product_batches (batch_no,product_id,plan_qty,status,operator,customer) VALUES (?,?,?,?,?,?)`,
		b.BatchNo, b.ProductID, b.PlanQty, b.Status, b.Operator, b.Customer,
	)
	if err != nil {
		return 0, TranslateError(err)
	}
	return res.LastInsertId()
}

func (t *SQLiteTx) UpdateBatchProduced(id int64, qty int) error {
	_, err := t.q.Exec(`UPDATE product_batches SET produced_qty=?, updated_at=(strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id=?`, qty, id)
	return err
}

func (t *SQLiteTx) MarkBatchConsumptionRecorded(id int64) error {
	_, err := t.q.Exec(`UPDATE product_batches SET consumption_recorded=1, updated_at=(strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id=?`, id)
	return err
}

func (t *SQLiteTx) UpdateBatchStatusFrom(id int64, status int, operator string, fromStatus int) (int64, error) {
	res, err := t.q.Exec(
		`UPDATE product_batches SET status=?, operator=?, version=version+1, updated_at=(strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id=? AND status=?`,
		status, operator, id, fromStatus,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (t *SQLiteTx) CreateAuditLog(log *model.AuditLog) error {
	_, err := t.q.Exec(
		`INSERT INTO audit_log (table_name,record_id,action,old_data,new_data,operator) VALUES (?,?,?,?,?,?)`,
		log.TableName, log.RecordID, log.Action, toRawJSON(log.OldData), toRawJSON(log.NewData), log.Operator,
	)
	return err
}

func (t *SQLiteTx) CreateBatchConsumption(c *model.BatchConsumption) error {
	_, err := t.q.Exec(
		`INSERT INTO batch_consumptions (batch_id,part_id,consumed_qty) VALUES (?,?,?)`,
		c.BatchID, c.PartID, c.ConsumedQty,
	)
	return err
}

func (t *SQLiteTx) CreateTrace(trace *model.BatchTrace) (int64, error) {
	res, err := t.q.Exec(
		`INSERT INTO batch_trace (batch_id,part_id,part_batch_no,used_qty,supplier,operator) VALUES (?,?,?,?,?,?)`,
		trace.BatchID, trace.PartID, trace.PartBatchNo, trace.UsedQty, trace.Supplier, trace.Operator,
	)
	if err != nil {
		return 0, TranslateError(err)
	}
	return res.LastInsertId()
}

func (t *SQLiteTx) ListBatchConsumptions(batchID int64) ([]model.BatchConsumption, error) {
	var rows []model.BatchConsumption
	err := t.q.Select(&rows,
		`SELECT part_id, consumed_qty FROM batch_consumptions WHERE batch_id=? ORDER BY part_id`,
		batchID)
	return rows, err
}

func (t *SQLiteTx) GetSkippedParts(batchID int64) ([]int64, error) {
	var ids []int64
	err := t.q.Select(&ids, `SELECT part_id FROM batch_skip_parts WHERE batch_id=?`, batchID)
	return ids, err
}

func (t *SQLiteTx) AddSkipPart(batchID, partID int64) error {
	_, err := t.q.Exec(`INSERT OR IGNORE INTO batch_skip_parts (batch_id,part_id) VALUES (?,?)`, batchID, partID)
	return err
}

func (t *SQLiteTx) RemoveSkipPart(batchID, partID int64) error {
	_, err := t.q.Exec(`DELETE FROM batch_skip_parts WHERE batch_id=? AND part_id=?`, batchID, partID)
	return err
}

type connQueryer struct{ c *sqlx.Conn }

func (q connQueryer) Get(dest any, query string, args ...any) error {
	return q.c.GetContext(context.Background(), dest, query, args...)
}
func (q connQueryer) Select(dest any, query string, args ...any) error {
	return q.c.SelectContext(context.Background(), dest, query, args...)
}
func (q connQueryer) Exec(query string, args ...any) (sql.Result, error) {
	return q.c.ExecContext(context.Background(), query, args...)
}

type queryer interface {
	Get(dest any, query string, args ...any) error
	Select(dest any, query string, args ...any) error
	Exec(query string, args ...any) (sql.Result, error)
}

func getProduct(q queryer, id int64) (*model.Product, error) {
	var p model.Product
	err := q.Get(&p, `SELECT * FROM products WHERE id=?`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func getBOM(q queryer, productID int64) ([]model.BOMItem, error) {
	var list []model.BOMItem
	err := q.Select(&list, `
		SELECT b.*, p.code AS part_code, p.name AS part_name
		FROM bom_items b
		JOIN parts p ON p.id = b.part_id
		WHERE b.product_id = ?
		ORDER BY b.part_id, b.id`, productID)
	return list, err
}

// ---- Store-level methods (thin wrappers / shared SQL) ----

func (s *SQLiteStore) GetProduct(id int64) (*model.Product, error) { return getProduct(s.db, id) }
func (s *SQLiteStore) ListProducts() ([]model.Product, error) {
	var list []model.Product
	err := s.db.Select(&list, `SELECT * FROM products ORDER BY id`)
	return list, err
}

func (s *SQLiteStore) GetPart(id int64) (*model.Part, error) {
	var p model.Part
	err := s.db.Get(&p, `SELECT * FROM parts WHERE id=?`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *SQLiteStore) ListParts() ([]model.Part, error) {
	var list []model.Part
	err := s.db.Select(&list, `SELECT * FROM parts ORDER BY id`)
	return list, err
}

func (s *SQLiteStore) GetBOMByProduct(productID int64) ([]model.BOMItem, error) {
	return getBOM(s.db, productID)
}

func (s *SQLiteStore) ListBatches() ([]model.ProductBatch, error) {
	var list []model.ProductBatch
	err := s.db.Select(&list, `
		SELECT b.*, p.name AS product_name, p.code AS product_code
		FROM product_batches b
		JOIN products p ON p.id = b.product_id
		ORDER BY b.id DESC`)
	return list, err
}

func (s *SQLiteStore) GetSkippedParts(batchID int64) ([]int64, error) {
	var ids []int64
	err := s.db.Select(&ids, `SELECT part_id FROM batch_skip_parts WHERE batch_id=?`, batchID)
	return ids, err
}

func (s *SQLiteStore) GetAllSkippedParts() ([]SkipPartRow, error) {
	var rows []SkipPartRow
	err := s.db.Select(&rows, `SELECT batch_id, part_id FROM batch_skip_parts ORDER BY batch_id`)
	return rows, err
}

func (s *SQLiteStore) CreateTrace(tr *model.BatchTrace) (int64, error) {
	var id int64
	err := s.WithTx(func(tx TxOps) error {
		n, err := tx.CreateTrace(tr)
		if err != nil {
			return err
		}
		id = n
		return nil
	})
	return id, err
}

func (s *SQLiteStore) GetTraceByBatch(batchID int64) ([]model.BatchTrace, error) {
	var list []model.BatchTrace
	err := s.db.Select(&list, `SELECT * FROM batch_trace WHERE batch_id=?`, batchID)
	return list, err
}

const sqliteAuditDateRangePredicate = `(
	(substr(created_at, 11, 1) = 'T' AND created_at >= ? AND created_at < ?)
	OR (substr(created_at, 11, 1) = ' ' AND created_at >= ? AND created_at < ?)
)`

// Native SQLite dates use UTC RFC3339 text; imported MySQL DATETIME values are
// naive local wall time, so each representation needs matching range bounds.
func sqliteAuditDateRangeArgs(start, end time.Time) []any {
	return []any{
		ceilSQLiteDateBound(start, time.Millisecond).UTC().Format("2006-01-02T15:04:05.000Z"),
		ceilSQLiteDateBound(end, time.Millisecond).UTC().Format("2006-01-02T15:04:05.000Z"),
		ceilSQLiteDateBound(start, time.Second).In(time.Local).Format("2006-01-02 15:04:05"),
		ceilSQLiteDateBound(end, time.Second).In(time.Local).Format("2006-01-02 15:04:05"),
	}
}

// ceilSQLiteDateBound rounds up to the stored precision so inclusive-start and
// exclusive-end ranges remain correct for the discrete timestamps on disk.
func ceilSQLiteDateBound(t time.Time, precision time.Duration) time.Time {
	bound := t.Truncate(precision)
	if bound.Before(t) {
		return bound.Add(precision)
	}
	return bound
}

func (s *SQLiteStore) ListStockLogsByDate(start, end time.Time, actions []string) ([]model.AuditLog, error) {
	if len(actions) == 0 {
		return nil, nil
	}
	ph := make([]string, len(actions))
	args := sqliteAuditDateRangeArgs(start, end)
	for i, a := range actions {
		ph[i] = "?"
		args = append(args, a)
	}
	q := `SELECT * FROM audit_log WHERE ` + sqliteAuditDateRangePredicate + ` AND action IN (` +
		strings.Join(ph, ",") + `) ORDER BY id ASC`
	var rows []auditLogRow
	if err := s.db.Select(&rows, q, args...); err != nil {
		return nil, err
	}
	out := make([]model.AuditLog, len(rows))
	for i, row := range rows {
		out[i] = toAuditModel(row)
	}
	return out, nil
}

func (s *SQLiteStore) ListAuditLogsByDate(start, end time.Time) ([]model.AuditLog, error) {
	var rows []auditLogRow
	err := s.db.Select(&rows,
		`SELECT * FROM audit_log WHERE `+sqliteAuditDateRangePredicate+` ORDER BY id DESC`,
		sqliteAuditDateRangeArgs(start, end)...)
	if err != nil {
		return nil, err
	}
	out := make([]model.AuditLog, len(rows))
	for i, row := range rows {
		out[i] = toAuditModel(row)
	}
	return out, nil
}

func (s *SQLiteStore) ListAuditLogs(tableName string, recordID int64) ([]model.AuditLog, error) {
	var rows []auditLogRow
	err := s.db.Select(&rows,
		`SELECT * FROM audit_log WHERE table_name=? AND record_id=? ORDER BY id DESC`, tableName, recordID)
	if err != nil {
		return nil, err
	}
	out := make([]model.AuditLog, len(rows))
	for i, row := range rows {
		out[i] = toAuditModel(row)
	}
	return out, nil
}

func (s *SQLiteStore) ListRecentAuditLogs(limit int) ([]model.AuditLog, error) {
	var rows []auditLogRow
	err := s.db.Select(&rows, `SELECT * FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	out := make([]model.AuditLog, len(rows))
	for i, row := range rows {
		out[i] = toAuditModel(row)
	}
	return out, nil
}

func (s *SQLiteStore) CreateAuditLog(log *model.AuditLog) error {
	_, err := s.db.Exec(
		`INSERT INTO audit_log (table_name,record_id,action,old_data,new_data,operator) VALUES (?,?,?,?,?,?)`,
		log.TableName, log.RecordID, log.Action, toRawJSON(log.OldData), toRawJSON(log.NewData), log.Operator,
	)
	return err
}

func (s *SQLiteStore) ListAllBOMItems() ([]model.BOMItem, error) {
	var list []model.BOMItem
	err := s.db.Select(&list, `
		SELECT b.*, p.code AS part_code, p.name AS part_name
		FROM bom_items b
		JOIN parts p ON p.id = b.part_id
		ORDER BY b.product_id, b.id`)
	return list, err
}

func (s *SQLiteStore) ListAllBatchConsumptions() ([]model.BatchConsumption, error) {
	var rows []model.BatchConsumption
	err := s.db.Select(&rows,
		`SELECT batch_id, part_id, consumed_qty FROM batch_consumptions ORDER BY batch_id, part_id`)
	return rows, err
}

func (s *SQLiteStore) ListAllBatchTraces() ([]model.BatchTrace, error) {
	var list []model.BatchTrace
	err := s.db.Select(&list, `SELECT * FROM batch_trace ORDER BY id`)
	return list, err
}

func (s *SQLiteStore) CountUsers() (int, error) {
	var n int
	err := s.db.Get(&n, `SELECT COUNT(*) FROM users`)
	return n, err
}

func (s *SQLiteStore) CountActiveAdmins() (int, error) {
	var n int
	err := s.db.Get(&n, `SELECT COUNT(*) FROM users WHERE role='admin' AND status=1`)
	return n, err
}

func (s *SQLiteStore) GetUserByUsername(username string) (*model.User, error) {
	var u model.User
	err := s.db.Get(&u, `SELECT * FROM users WHERE username=?`, username)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *SQLiteStore) GetUserByID(id int64) (*model.User, error) {
	var u model.User
	err := s.db.Get(&u, `SELECT * FROM users WHERE id=?`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *SQLiteStore) ListUsers() ([]model.User, error) {
	var list []model.User
	err := s.db.Select(&list, `SELECT * FROM users ORDER BY id`)
	return list, err
}

func (s *SQLiteStore) CreateUser(u *model.User) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO users (username,password_hash,display_name,role,status) VALUES (?,?,?,?,?)`,
		u.Username, u.PasswordHash, u.DisplayName, u.Role, u.Status,
	)
	if err != nil {
		return 0, TranslateError(err)
	}
	return res.LastInsertId()
}

func (s *SQLiteStore) UpdateUser(u *model.User) error {
	_, err := s.db.Exec(
		`UPDATE users SET display_name=?, role=?, status=?, updated_at=(strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id=?`,
		u.DisplayName, u.Role, u.Status, u.ID,
	)
	return err
}

func (s *SQLiteStore) UpdateUserPassword(id int64, hash string) error {
	_, err := s.db.Exec(`UPDATE users SET password_hash=?, updated_at=(strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id=?`, hash, id)
	return err
}

func (s *SQLiteStore) DeleteUser(id int64) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

func (s *SQLiteStore) TouchUserLogin(id int64) error {
	_, err := s.db.Exec(`UPDATE users SET last_login_at=(strftime('%Y-%m-%dT%H:%M:%fZ','now')) WHERE id=?`, id)
	return err
}

// ---- 数据库身份（D-016 接入服务器前期准备）----

// GetDatabaseID 返回 db_identity 的稳定标识；表/行缺失时返回空串（不报错）。
func (s *SQLiteStore) GetDatabaseID() (string, error) {
	var id string
	err := s.db.Get(&id, `SELECT id FROM db_identity LIMIT 1`)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// GetSchemaVersion 返回 schema_migrations 里的最高版本。
func (s *SQLiteStore) GetSchemaVersion() (int, error) {
	var v sql.NullInt64
	if err := s.db.Get(&v, `SELECT MAX(version) FROM schema_migrations`); err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return int(v.Int64), nil
}
