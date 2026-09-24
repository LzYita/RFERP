package repository

import (
	"context"
	"database/sql"
	"fmt"
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
// The zero field q is a *sqlx.Conn that has BEGIN IMMEDIATE open.
type SQLiteTx struct {
	q queryer
}

// OpenSQLite opens (creating if needed) a SQLite database with product PRAGMAs.
func OpenSQLite(path string) (*sqlx.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&parseTime=true"
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
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), `PRAGMA defer_foreign_keys = ON`); err != nil {
		return fmt.Errorf("defer foreign keys: %w", err)
	}
	err = withImmediateConn(conn, fn)
	_, _ = conn.ExecContext(context.Background(), `PRAGMA defer_foreign_keys = OFF`)
	return err
}

func (s *SQLiteStore) withImmediateTx(fn func(TxOps) error) error {
	conn, err := s.db.Connx(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	return withImmediateConn(conn, fn)
}

func withImmediateConn(conn *sqlx.Conn, fn func(TxOps) error) error {
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("begin immediate: %w", err)
	}
	tx := &SQLiteTx{q: connQueryer{conn}}
	if err := fn(tx); err != nil {
		_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		_, _ = conn.ExecContext(ctx, `ROLLBACK`)
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (t *SQLiteTx) Exec(query string, args ...any) (sql.Result, error) {
	return t.q.Exec(query, args...)
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
		return 0, err
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
		return 0, err
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

func (s *SQLiteStore) ListStockLogsByDate(start, end time.Time, actions []string) ([]model.AuditLog, error) {
	if len(actions) == 0 {
		return nil, nil
	}
	ph := make([]string, len(actions))
	args := make([]any, 0, len(actions)+2)
	args = append(args, start, end)
	for i, a := range actions {
		ph[i] = "?"
		args = append(args, a)
	}
	q := `SELECT * FROM audit_log WHERE created_at >= ? AND created_at < ? AND action IN (` +
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
		`SELECT * FROM audit_log WHERE created_at >= ? AND created_at < ? ORDER BY id DESC`, start, end)
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
