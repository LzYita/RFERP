// Package usecase 定义应用用例接口与适配端口。
//
// # A1 用例清单（2026-09-23）
//
// | 领域 | 用例 | 现入口 |
// |---|---|---|
// | Identity | Login / UserCount / CreateInitialAdmin / CreateUser / ListUsers / UpdateUser / ResetPassword / ChangePassword / DeleteUser | ui:login,ui:users |
// | Catalog | Create/Get/List/Update/Delete Product；Create/Get/List/Update/Delete Part；Add/Get/Remove BOM；ValidateBOM | ui:product,ui:part,ui:bom |
// | Inventory | StockIn / AdjustStock | ui:part |
// | Production | CreateBatch / ListBatches / UpdateBatchStatus / RevokeBatch / AddSkipPart / RemoveSkipPart / GetSkippedParts | ui:batch |
// | Trace | RecordTrace / RecordTraces / GetTraceByBatch / TraceByProduct / TraceByPart | ui:batch,ui:dashboard |
// | Audit | GetAuditLogs / ListRecentAuditLogs | ui:audit |
// | Stats | GetStockStats | ui:stats,ui:dashboard |
// | Backup | BackupDatabase / RestoreDatabase / ClearDatabase / ExportAuditLogCSV / ExportAllDataCSV / DataDir / SetDataDir | ui:backup |
//
// 接口按用例/领域命名，不按页面命名。UI 与未来 HTTP API 均依赖本包接口；
// 具体实现在 internal/service，由 cmd 装配。
//
// 可移植性：业务实现（internal/service）禁止 MySQL 方言与按库分支；
// SQL、dump、会话级 FOREIGN_KEY_CHECKS 等只允许出现在适配层（repository/dbbackup/migrate）。
package usecase

import (
	"time"

	"app/internal/model"
)

// Identity 账号、角色与登录。
type Identity interface {
	UserCount() (int, error)
	Login(username, password string) (*model.User, error)
	CreateInitialAdmin(username, password, displayName string) (*model.User, error)
	CreateUser(in CreateUserInput) (*model.User, error)
	ListUsers() ([]model.User, error)
	UpdateUser(in UpdateUserInput) error
	ResetPassword(in ResetPasswordInput) error
	ChangePassword(in ChangePasswordInput) error
	DeleteUser(id int64) error
}

// Catalog 产品、零件与 BOM。
type Catalog interface {
	CreateProduct(p *model.Product) (*model.Product, error)
	GetProduct(id int64) (*model.Product, error)
	ListProducts() ([]model.Product, error)
	UpdateProduct(p *model.Product) (*model.Product, error)
	DeleteProduct(id int64, operator string) error

	CreatePart(p *model.Part) (*model.Part, error)
	GetPart(id int64) (*model.Part, error)
	ListParts() ([]model.Part, error)
	UpdatePart(p *model.Part) (*model.Part, error)
	DeletePart(id int64, operator string) error

	AddBOMItem(b *model.BOMItem) (*model.BOMItem, error)
	GetBOMByProduct(productID int64) ([]model.BOMItem, error)
	RemoveBOMItem(id int64, operator string) error
	ValidateBOM(productID int64) (bool, error)
}

// Inventory 零件库存出入与盘点。
type Inventory interface {
	StockIn(in StockInInput) error
	AdjustStock(in AdjustStockInput) error
}

// Production 生产批次与投料跳过零件。
type Production interface {
	CreateBatch(b *model.ProductBatch) (*model.ProductBatch, error)
	ListBatches() ([]model.ProductBatch, error)
	UpdateBatchStatus(in UpdateBatchStatusInput) error
	RevokeBatch(in RevokeBatchInput) error
	AddSkipPart(batchID, partID int64) error
	RemoveSkipPart(batchID, partID int64) error
	GetSkippedParts(batchID int64) ([]int64, error)
}

// Trace 批次正/反向追溯。
type Trace interface {
	RecordTrace(t *model.BatchTrace) (*model.BatchTrace, error)
	RecordTraces(traces []*model.BatchTrace) error
	GetTraceByBatch(batchID int64) ([]model.BatchTrace, error)
	TraceByProduct(batchNo string) ([]model.BatchTrace, error)
	TraceByPart(partCode string) ([]model.BatchTrace, error)
}

// Audit 操作审计查询。
type Audit interface {
	GetAuditLogs(tableName string, recordID int64) ([]model.AuditLog, error)
	ListRecentAuditLogs(limit int) ([]model.AuditLog, error)
}

// Stats 统计查询。
type Stats interface {
	GetStockStats(days int) (*StockStats, error)
}

// Backup 备份、恢复、清库与导出。语义见 D-014：单份完整快照，不合成。
type Backup interface {
	DataDir() string
	SetDataDir(dir string) error
	BackupDatabase(saveDir string) (string, error)
	RestoreDatabase(filePath string) (success, failed int, err error)
	ClearDatabase() error
	ExportAuditLogCSV(startDate, endDate time.Time, filePath string) (int, error)
	ExportAllDataCSV(saveDir string) (map[string]string, string, error)
}

// Applications 桌面/API 完整用例集合。
type Applications interface {
	Identity
	Catalog
	Inventory
	Production
	Trace
	Audit
	Stats
	Backup
}
