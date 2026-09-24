package repository

import (
	"database/sql"
	"time"

	"app/internal/model"
)

// SkipPartRow is one batch-skip-part edge (exported for Store contracts).
type SkipPartRow struct {
	BatchID int64 `db:"batch_id"`
	PartID  int64 `db:"part_id"`
}

// TxOps is the transaction-scoped persistence surface used inside WithTx /
// WithBulkLoad. MySQL (*Tx) and SQLite (*SQLiteTx) both implement it.
// "ForUpdate" methods lock on MySQL; on SQLite they are plain reads under the
// write transaction (single-writer).
type TxOps interface {
	Exec(query string, args ...any) (sql.Result, error)

	GetProduct(id int64) (*model.Product, error)
	GetProductForUpdate(id int64) (*model.Product, error)
	CreateProduct(p *model.Product) (int64, error)
	UpdateProduct(p *model.Product) (int64, error)
	DeleteProduct(id int64) error

	CreatePart(p *model.Part) (int64, error)
	UpdatePart(p *model.Part) (int64, error)
	DeletePart(id int64) error
	GetPartForUpdate(id int64) (*model.Part, error)
	UpdatePartStock(partID int64, newQty float64) error

	GetBOMByProduct(productID int64) ([]model.BOMItem, error)
	CreateBOMItem(b *model.BOMItem) (int64, error)
	DeleteBOMItem(id int64) error

	GetBatchForUpdate(id int64) (*model.ProductBatch, error)
	CreateBatch(b *model.ProductBatch) (int64, error)
	UpdateBatchProduced(id int64, qty int) error
	MarkBatchConsumptionRecorded(id int64) error
	UpdateBatchStatusFrom(id int64, status int, operator string, fromStatus int) (int64, error)

	CreateAuditLog(log *model.AuditLog) error
	CreateBatchConsumption(c *model.BatchConsumption) error
	CreateTrace(trace *model.BatchTrace) (int64, error)
	ListBatchConsumptions(batchID int64) ([]model.BatchConsumption, error)
	GetSkippedParts(batchID int64) ([]int64, error)
	AddSkipPart(batchID, partID int64) error
	RemoveSkipPart(batchID, partID int64) error
}

// Store is the persistence surface used by the application service.
type Store interface {
	WithTx(fn func(TxOps) error) error
	WithBulkLoad(fn func(TxOps) error) error

	GetProduct(id int64) (*model.Product, error)
	ListProducts() ([]model.Product, error)
	GetPart(id int64) (*model.Part, error)
	ListParts() ([]model.Part, error)
	GetBOMByProduct(productID int64) ([]model.BOMItem, error)

	ListBatches() ([]model.ProductBatch, error)
	GetSkippedParts(batchID int64) ([]int64, error)
	GetAllSkippedParts() ([]SkipPartRow, error)
	CreateTrace(t *model.BatchTrace) (int64, error)
	GetTraceByBatch(batchID int64) ([]model.BatchTrace, error)

	ListStockLogsByDate(start, end time.Time, actions []string) ([]model.AuditLog, error)
	ListAuditLogsByDate(start, end time.Time) ([]model.AuditLog, error)
	ListAuditLogs(tableName string, recordID int64) ([]model.AuditLog, error)
	ListRecentAuditLogs(limit int) ([]model.AuditLog, error)
	CreateAuditLog(log *model.AuditLog) error

	ListAllBOMItems() ([]model.BOMItem, error)
	ListAllBatchConsumptions() ([]model.BatchConsumption, error)
	ListAllBatchTraces() ([]model.BatchTrace, error)

	CountUsers() (int, error)
	CountActiveAdmins() (int, error)
	GetUserByUsername(username string) (*model.User, error)
	GetUserByID(id int64) (*model.User, error)
	ListUsers() ([]model.User, error)
	CreateUser(u *model.User) (int64, error)
	UpdateUser(u *model.User) error
	UpdateUserPassword(id int64, hash string) error
	DeleteUser(id int64) error
	TouchUserLogin(id int64) error
}

var (
	_ Store = (*Repository)(nil)
	_ TxOps = (*Tx)(nil)
	_ Store = (*SQLiteStore)(nil)
	_ TxOps = (*SQLiteTx)(nil)
)
