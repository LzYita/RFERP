package repository

import (
	"time"

	"app/internal/model"
)

// SkipPartRow is one batch-skip-part edge (exported for Store contracts).
type SkipPartRow struct {
	BatchID int64 `db:"batch_id"`
	PartID  int64 `db:"part_id"`
}

// Store is the persistence surface used by the application service.
// MySQL (*Repository) is the current implementation; SQLite will implement
// this same interface (Phase E). Dialect and drivers stay out of callers.
//
// Tx callbacks use the shared *Tx wrapper (sqlx transaction), which is
// driver-agnostic. Per-adapter SQL lives in each Store implementation.
type Store interface {
	WithTx(fn func(*Tx) error) error
	WithBulkLoad(fn func(*Tx) error) error

	// Catalog / inventory reads
	GetProduct(id int64) (*model.Product, error)
	ListProducts() ([]model.Product, error)
	GetPart(id int64) (*model.Part, error)
	ListParts() ([]model.Part, error)
	GetBOMByProduct(productID int64) ([]model.BOMItem, error)

	// Production / trace reads
	ListBatches() ([]model.ProductBatch, error)
	GetSkippedParts(batchID int64) ([]int64, error)
	GetAllSkippedParts() ([]SkipPartRow, error)
	CreateTrace(t *model.BatchTrace) (int64, error)
	GetTraceByBatch(batchID int64) ([]model.BatchTrace, error)

	// Audit / stats reads
	ListStockLogsByDate(start, end time.Time, actions []string) ([]model.AuditLog, error)
	ListAuditLogsByDate(start, end time.Time) ([]model.AuditLog, error)
	ListAuditLogs(tableName string, recordID int64) ([]model.AuditLog, error)
	ListRecentAuditLogs(limit int) ([]model.AuditLog, error)
	CreateAuditLog(log *model.AuditLog) error

	// Bulk export reads
	ListAllBOMItems() ([]model.BOMItem, error)
	ListAllBatchConsumptions() ([]model.BatchConsumption, error)
	ListAllBatchTraces() ([]model.BatchTrace, error)

	// Identity
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

// Compile-time check: the MySQL repository satisfies Store.
var _ Store = (*Repository)(nil)
