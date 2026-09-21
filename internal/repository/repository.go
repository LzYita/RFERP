package repository

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"app/internal/model"
)

type Repository struct {
	db *sqlx.DB
}

// Tx exposes repository operations that participate in one atomic state
// transition. The transaction is owned by Repository.WithTx.
type Tx struct {
	tx *sqlx.Tx
}

func New(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// WithTx runs fn in a transaction and commits only when fn succeeds. Callers
// use the transaction for every read-modify-write and audit operation that
// must share one commit boundary.
func (r *Repository) WithTx(fn func(*Tx) error) error {
	tx, err := r.db.Beginx()
	if err != nil {
		return err
	}

	if err := fn(&Tx{tx: tx}); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			return fmt.Errorf("%w (rollback failed: %v)", err, rollbackErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch transaction: %w", err)
	}
	return nil
}

// ---- 产品 ----

func (r *Repository) CreateProduct(p *model.Product) (int64, error) {
	res, err := r.db.Exec(
		`INSERT INTO products (code,name,spec,unit,status,operator) VALUES (?,?,?,?,?,?)`,
		p.Code, p.Name, p.Spec, p.Unit, p.Status, p.Operator,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repository) GetProduct(id int64) (*model.Product, error) {
	var p model.Product
	err := r.db.Get(&p, `SELECT * FROM products WHERE id=?`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *Repository) ListProducts() ([]model.Product, error) {
	var list []model.Product
	err := r.db.Select(&list, `SELECT * FROM products ORDER BY id`)
	return list, err
}

func (r *Repository) UpdateProduct(p *model.Product) (int64, error) {
	res, err := r.db.Exec(
		`UPDATE products SET code=?,name=?,spec=?,unit=?,status=?,operator=?, version=version+1 WHERE id=? AND version=?`,
		p.Code, p.Name, p.Spec, p.Unit, p.Status, p.Operator, p.ID, p.Version,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *Repository) DeleteProduct(id int64) error {
	_, err := r.db.Exec(`DELETE FROM products WHERE id=?`, id)
	return err
}

// ---- 零件 ----

func (r *Repository) CreatePart(p *model.Part) (int64, error) {
	res, err := r.db.Exec(
		`INSERT INTO parts (code,name,spec,unit,part_type,stock_qty,warn_qty,status,operator,supplier) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		p.Code, p.Name, p.Spec, p.Unit, p.PartType, p.StockQty, p.WarnQty, p.Status, p.Operator, p.Supplier,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repository) GetPart(id int64) (*model.Part, error) {
	var p model.Part
	err := r.db.Get(&p, `SELECT * FROM parts WHERE id=?`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *Repository) ListParts() ([]model.Part, error) {
	var list []model.Part
	err := r.db.Select(&list, `SELECT * FROM parts ORDER BY id`)
	return list, err
}

func (r *Repository) UpdatePart(p *model.Part) (int64, error) {
	res, err := r.db.Exec(
		`UPDATE parts SET code=?,name=?,spec=?,unit=?,part_type=?,stock_qty=?,warn_qty=?,status=?,operator=?,supplier=?, version=version+1 WHERE id=? AND version=?`,
		p.Code, p.Name, p.Spec, p.Unit, p.PartType, p.StockQty, p.WarnQty, p.Status, p.Operator, p.Supplier, p.ID, p.Version,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *Repository) DeletePart(id int64) error {
	_, err := r.db.Exec(`DELETE FROM parts WHERE id=?`, id)
	return err
}

// ---- BOM ----

func (r *Repository) CreateBOMItem(b *model.BOMItem) (int64, error) {
	res, err := r.db.Exec(
		`INSERT INTO bom_items (product_id,part_id,quantity,loss_rate,remark,operator,replaceable,use_mode) VALUES (?,?,?,?,?,?,?,?)`,
		b.ProductID, b.PartID, b.Quantity, b.LossRate, b.Remark, b.Operator, b.Replaceable, b.UseMode,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repository) GetBOMByProduct(productID int64) ([]model.BOMItem, error) {
	var list []model.BOMItem
	err := r.db.Select(&list, `
		SELECT b.*, p.code AS part_code, p.name AS part_name
		FROM bom_items b
		JOIN parts p ON p.id = b.part_id
		WHERE b.product_id = ?
		ORDER BY b.part_id, b.id`, productID)
	return list, err
}

func (r *Repository) DeleteBOMItem(id int64) error {
	_, err := r.db.Exec(`DELETE FROM bom_items WHERE id=?`, id)
	return err
}

// ---- 批次 ----

func (r *Repository) DeleteTraceByBatch(batchID int64) error {
	_, err := r.db.Exec(`DELETE FROM batch_trace WHERE batch_id=?`, batchID)
	return err
}

func (r *Repository) DeleteBatch(id int64) error {
	_, err := r.db.Exec(`DELETE FROM product_batches WHERE id=?`, id)
	return err
}

func (r *Repository) GetAllSkippedParts() ([]struct {
	BatchID int64 `db:"batch_id"`
	PartID  int64 `db:"part_id"`
}, error) {
	var rows []struct {
		BatchID int64 `db:"batch_id"`
		PartID  int64 `db:"part_id"`
	}
	err := r.db.Select(&rows, `SELECT batch_id, part_id FROM batch_skip_parts ORDER BY batch_id`)
	return rows, err
}

func (r *Repository) GetSkippedParts(batchID int64) ([]int64, error) {
	var ids []int64
	err := r.db.Select(&ids, `SELECT part_id FROM batch_skip_parts WHERE batch_id=?`, batchID)
	return ids, err
}

func (r *Repository) AddSkipPart(batchID, partID int64) error {
	return r.WithTx(func(tx *Tx) error {
		batch, err := tx.GetBatchForUpdate(batchID)
		if err != nil {
			return err
		}
		if batch == nil {
			return fmt.Errorf("batch not found")
		}
		if batch.Status == 2 || batch.Status == 4 {
			return fmt.Errorf("completed or revoked batch cannot change skipped parts")
		}
		return tx.AddSkipPart(batchID, partID)
	})
}

func (r *Repository) RemoveSkipPart(batchID, partID int64) error {
	return r.WithTx(func(tx *Tx) error {
		batch, err := tx.GetBatchForUpdate(batchID)
		if err != nil {
			return err
		}
		if batch == nil {
			return fmt.Errorf("batch not found")
		}
		if batch.Status == 2 || batch.Status == 4 {
			return fmt.Errorf("completed or revoked batch cannot change skipped parts")
		}
		return tx.RemoveSkipPart(batchID, partID)
	})
}

func (r *Repository) CreateBatch(b *model.ProductBatch) (int64, error) {
	res, err := r.db.Exec(
		`INSERT INTO product_batches (batch_no,product_id,plan_qty,status,operator,customer) VALUES (?,?,?,?,?,?)`,
		b.BatchNo, b.ProductID, b.PlanQty, b.Status, b.Operator, b.Customer,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repository) GetBatch(id int64) (*model.ProductBatch, error) {
	var b model.ProductBatch
	err := r.db.Get(&b, `SELECT * FROM product_batches WHERE id=?`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

func (r *Repository) ListBatches() ([]model.ProductBatch, error) {
	var list []model.ProductBatch
	err := r.db.Select(&list, `
		SELECT b.*, p.name AS product_name, p.code AS product_code
		FROM product_batches b
		JOIN products p ON p.id = b.product_id
		ORDER BY b.id DESC`)
	return list, err
}

func (r *Repository) UpdatePartStock(partID int64, newQty float64) error {
	return r.WithTx(func(tx *Tx) error {
		part, err := tx.GetPartForUpdate(partID)
		if err != nil {
			return err
		}
		if part == nil {
			return fmt.Errorf("part not found")
		}
		return tx.UpdatePartStock(partID, newQty)
	})
}

func (r *Repository) UpdateBatchProduced(id int64, qty int) error {
	_, err := r.db.Exec(`UPDATE product_batches SET produced_qty=? WHERE id=?`, qty, id)
	return err
}

func (r *Repository) UpdateBatchStatus(id int64, status int, operator string) (int64, error) {
	res, err := r.db.Exec(
		`UPDATE product_batches SET status=?, operator=? WHERE id=?`,
		status, operator, id,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (t *Tx) GetBatchForUpdate(id int64) (*model.ProductBatch, error) {
	var b model.ProductBatch
	err := t.tx.Get(&b, `SELECT * FROM product_batches WHERE id=? FOR UPDATE`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

func (t *Tx) GetProduct(id int64) (*model.Product, error) {
	var p model.Product
	err := t.tx.Get(&p, `SELECT * FROM products WHERE id=?`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (t *Tx) GetBOMByProduct(productID int64) ([]model.BOMItem, error) {
	var list []model.BOMItem
	err := t.tx.Select(&list, `
		SELECT b.*, p.code AS part_code, p.name AS part_name
		FROM bom_items b
		JOIN parts p ON p.id = b.part_id
		WHERE b.product_id = ?
		ORDER BY b.part_id, b.id`, productID)
	return list, err
}

func (t *Tx) GetSkippedParts(batchID int64) ([]int64, error) {
	var ids []int64
	err := t.tx.Select(&ids, `SELECT part_id FROM batch_skip_parts WHERE batch_id=?`, batchID)
	return ids, err
}

func (t *Tx) GetPartForUpdate(id int64) (*model.Part, error) {
	var p model.Part
	err := t.tx.Get(&p, `SELECT * FROM parts WHERE id=? FOR UPDATE`, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (t *Tx) UpdatePartStock(partID int64, newQty float64) error {
	_, err := t.tx.Exec(`UPDATE parts SET stock_qty=?, version=version+1 WHERE id=?`, newQty, partID)
	if err != nil {
		return err
	}
	return nil
}

func (t *Tx) UpdateBatchProduced(id int64, qty int) error {
	_, err := t.tx.Exec(`UPDATE product_batches SET produced_qty=? WHERE id=?`, qty, id)
	if err != nil {
		return err
	}
	return nil
}

func (t *Tx) MarkBatchConsumptionRecorded(id int64) error {
	_, err := t.tx.Exec(`UPDATE product_batches SET consumption_recorded=1 WHERE id=?`, id)
	return err
}

func (t *Tx) UpdateBatchStatusFrom(id int64, status int, operator string, fromStatus int) (int64, error) {
	res, err := t.tx.Exec(
		`UPDATE product_batches SET status=?, operator=?, version=version+1 WHERE id=? AND status=?`,
		status, operator, id, fromStatus,
	)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

func (t *Tx) CreateAuditLog(log *model.AuditLog) error {
	oldJSON := toRawJSON(log.OldData)
	newJSON := toRawJSON(log.NewData)
	_, err := t.tx.Exec(
		`INSERT INTO audit_log (table_name,record_id,action,old_data,new_data,operator) VALUES (?,?,?,?,?,?)`,
		log.TableName, log.RecordID, log.Action, oldJSON, newJSON, log.Operator,
	)
	return err
}

func (t *Tx) CreateBatchConsumption(c *model.BatchConsumption) error {
	_, err := t.tx.Exec(
		`INSERT INTO batch_consumptions (batch_id,part_id,consumed_qty) VALUES (?,?,?)`,
		c.BatchID, c.PartID, c.ConsumedQty,
	)
	return err
}

func (t *Tx) ListBatchConsumptions(batchID int64) ([]model.BatchConsumption, error) {
	var list []model.BatchConsumption
	err := t.tx.Select(&list,
		`SELECT part_id, consumed_qty FROM batch_consumptions WHERE batch_id=? ORDER BY part_id`,
		batchID)
	return list, err
}

func (t *Tx) AddSkipPart(batchID, partID int64) error {
	_, err := t.tx.Exec(`INSERT IGNORE INTO batch_skip_parts (batch_id,part_id) VALUES (?,?)`, batchID, partID)
	return err
}

func (t *Tx) RemoveSkipPart(batchID, partID int64) error {
	_, err := t.tx.Exec(`DELETE FROM batch_skip_parts WHERE batch_id=? AND part_id=?`, batchID, partID)
	return err
}

// ---- 追溯 ----

func (r *Repository) CreateTrace(t *model.BatchTrace) (int64, error) {
	res, err := r.db.Exec(
		`INSERT INTO batch_trace (batch_id,part_id,part_batch_no,used_qty,supplier,operator) VALUES (?,?,?,?,?,?)`,
		t.BatchID, t.PartID, t.PartBatchNo, t.UsedQty, t.Supplier, t.Operator,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repository) GetTraceByBatch(batchID int64) ([]model.BatchTrace, error) {
	var list []model.BatchTrace
	err := r.db.Select(&list, `SELECT * FROM batch_trace WHERE batch_id=?`, batchID)
	return list, err
}

func (r *Repository) GetTraceByPart(partID int64) ([]model.BatchTrace, error) {
	var list []model.BatchTrace
	err := r.db.Select(&list, `SELECT * FROM batch_trace WHERE part_id=?`, partID)
	return list, err
}

func (r *Repository) Exec(sql string) (sql.Result, error) {
	return r.db.Exec(sql)
}

// ---- 全部BOM与追溯 ----

func (r *Repository) ListAllBOMItems() ([]model.BOMItem, error) {
	var list []model.BOMItem
	err := r.db.Select(&list, `
		SELECT b.*, p.code AS part_code, p.name AS part_name
		FROM bom_items b
		JOIN parts p ON p.id = b.part_id
		ORDER BY b.product_id, b.id`)
	return list, err
}

func (r *Repository) ListAllBatchTraces() ([]model.BatchTrace, error) {
	var list []model.BatchTrace
	err := r.db.Select(&list, `SELECT * FROM batch_trace ORDER BY id`)
	return list, err
}

func (r *Repository) ListAuditLogsByDate(start, end time.Time) ([]model.AuditLog, error) {
	var rows []auditLogRow
	err := r.db.Select(&rows,
		`SELECT * FROM audit_log WHERE created_at >= ? AND created_at < ? ORDER BY id DESC`,
		start, end)
	if err != nil {
		return nil, err
	}
	out := make([]model.AuditLog, len(rows))
	for i, row := range rows {
		out[i] = toAuditModel(row)
	}
	return out, nil
}

// ListStockLogsByDate 查询指定时间范围内的库存出入库审计日志
// actions 如 STOCK_IN/STOCK_DEDUCT/STOCK_ADJUST
func (r *Repository) ListStockLogsByDate(start, end time.Time, actions []string) ([]model.AuditLog, error) {
	if len(actions) == 0 {
		return nil, nil
	}
	q := `SELECT * FROM audit_log WHERE created_at >= ? AND created_at < ? AND action IN (`
	args := []any{start, end}
	placeholders := ""
	for i, a := range actions {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, a)
	}
	q += placeholders + `) ORDER BY id ASC`
	var rows []auditLogRow
	if err := r.db.Select(&rows, q, args...); err != nil {
		return nil, err
	}
	out := make([]model.AuditLog, len(rows))
	for i, row := range rows {
		out[i] = toAuditModel(row)
	}
	return out, nil
}

// ---- 审计日志 ----

func (r *Repository) CreateAuditLog(log *model.AuditLog) error {
	oldJSON := toRawJSON(log.OldData)
	newJSON := toRawJSON(log.NewData)
	_, err := r.db.Exec(
		`INSERT INTO audit_log (table_name,record_id,action,old_data,new_data,operator) VALUES (?,?,?,?,?,?)`,
		log.TableName, log.RecordID, log.Action, oldJSON, newJSON, log.Operator,
	)
	return err
}

func toRawJSON(m *map[string]any) []byte {
	if m == nil {
		return nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

type auditLogRow struct {
	ID        int64     `db:"id"`
	TableName string    `db:"table_name"`
	RecordID  int64     `db:"record_id"`
	Action    string    `db:"action"`
	OldData   []byte    `db:"old_data"`
	NewData   []byte    `db:"new_data"`
	Operator  *string   `db:"operator"`
	CreatedAt time.Time `db:"created_at"`
}

func toAuditModel(row auditLogRow) model.AuditLog {
	a := model.AuditLog{
		ID:        row.ID,
		TableName: row.TableName,
		RecordID:  row.RecordID,
		Action:    row.Action,
		Operator:  row.Operator,
		CreatedAt: row.CreatedAt,
	}
	if len(row.OldData) > 0 {
		var m map[string]any
		if json.Unmarshal(row.OldData, &m) == nil {
			a.OldData = &m
		}
	}
	if len(row.NewData) > 0 {
		var m map[string]any
		if json.Unmarshal(row.NewData, &m) == nil {
			a.NewData = &m
		}
	}
	return a
}

func (r *Repository) ListAuditLogs(tableName string, recordID int64) ([]model.AuditLog, error) {
	var rows []auditLogRow
	err := r.db.Select(&rows,
		`SELECT * FROM audit_log WHERE table_name=? AND record_id=? ORDER BY id DESC`,
		tableName, recordID)
	if err != nil {
		return nil, err
	}
	out := make([]model.AuditLog, len(rows))
	for i, row := range rows {
		out[i] = toAuditModel(row)
	}
	return out, nil
}

func (r *Repository) ListRecentAuditLogs(limit int) ([]model.AuditLog, error) {
	var rows []auditLogRow
	err := r.db.Select(&rows,
		`SELECT * FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	out := make([]model.AuditLog, len(rows))
	for i, row := range rows {
		out[i] = toAuditModel(row)
	}
	return out, nil
}

// ---- 用户 ----

func (r *Repository) CountUsers() (int, error) {
	var n int
	err := r.db.Get(&n, `SELECT COUNT(*) FROM users`)
	return n, err
}

func (r *Repository) CountActiveAdmins() (int, error) {
	var n int
	err := r.db.Get(&n, `SELECT COUNT(*) FROM users WHERE role='admin' AND status=1`)
	return n, err
}

func (r *Repository) GetUserByUsername(username string) (*model.User, error) {
	var u model.User
	err := r.db.Get(&u, `SELECT * FROM users WHERE username=?`, username)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *Repository) GetUserByID(id int64) (*model.User, error) {
	var u model.User
	err := r.db.Get(&u, `SELECT * FROM users WHERE id=?`, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *Repository) ListUsers() ([]model.User, error) {
	var list []model.User
	err := r.db.Select(&list, `SELECT * FROM users ORDER BY id`)
	return list, err
}

func (r *Repository) CreateUser(u *model.User) (int64, error) {
	res, err := r.db.Exec(
		`INSERT INTO users (username,password_hash,display_name,role,status) VALUES (?,?,?,?,?)`,
		u.Username, u.PasswordHash, u.DisplayName, u.Role, u.Status)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repository) UpdateUser(u *model.User) error {
	_, err := r.db.Exec(
		`UPDATE users SET display_name=?, role=?, status=? WHERE id=?`,
		u.DisplayName, u.Role, u.Status, u.ID)
	return err
}

func (r *Repository) UpdateUserPassword(id int64, hash string) error {
	_, err := r.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, hash, id)
	return err
}

func (r *Repository) DeleteUser(id int64) error {
	_, err := r.db.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

func (r *Repository) TouchUserLogin(id int64) error {
	_, err := r.db.Exec(`UPDATE users SET last_login_at=NOW() WHERE id=?`, id)
	return err
}
