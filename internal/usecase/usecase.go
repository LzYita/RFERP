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
	UpdateBOMItem(b *model.BOMItem) (*model.BOMItem, error)
	GetBOMByProduct(productID int64) ([]model.BOMItem, error)
	RemoveBOMItem(id int64, operator string) error
	ValidateBOM(productID int64) (bool, error)

	// StockProjection 返回"未完成批次完成后"的库存视图（只读，不写库）。
	StockProjection(in StockProjectionInput) (*StockProjectionResult, error)
}

// Inventory 零件库存出入与盘点。
type Inventory interface {
	// StockIn 入库并自动生成入库批次号（返回值即批次号，界面用来回显）。
	StockIn(in StockInInput) (string, error)
	AdjustStock(in AdjustStockInput) error
	// AdjustInbounds 逐批次盘点：按批次实盘数量改写剩余，并同步库存总量。
	AdjustInbounds(in AdjustInboundsInput) error
	// ListPartInbounds 零件的入库批次列表（零件详情页）。
	ListPartInbounds(partID int64) ([]model.PartInbound, error)
}

// Production 生产批次、投料跳过零件与临时零件。
type Production interface {
	CreateBatch(b *model.ProductBatch) (*model.ProductBatch, error)
	ListBatches() ([]model.ProductBatch, error)
	UpdateBatchStatus(in UpdateBatchStatusInput) error
	RevokeBatch(in RevokeBatchInput) error
	AddSkipPart(batchID, partID int64, operator string) error
	RemoveSkipPart(batchID, partID int64, operator string) error
	GetSkippedParts(batchID int64) ([]int64, error)
	ListTempParts(batchID int64) ([]model.BatchTempPart, error)
	AddTempPart(in AddTempPartInput) error
	RemoveTempPart(batchID, partID int64, operator string) error
}

// AddTempPartInput 添加（或改）批次临时零件。推导三件套必须同时给出或
// 同时为空：只给一部分说明调用方把表单拆坏了，静默接受会让"总量怎么来的"
// 再也说不清。
type AddTempPartInput struct {
	BatchID  int64
	PartID   int64
	Qty      float64
	UseMode  *int
	Quantity *float64
	LossRate *float64
	// Operator 由 API 层从会话填入，用于审计。
	Operator string
}

// Trace 批次正/反向追溯。
type Trace interface {
	RecordTrace(t *model.BatchTrace) (*model.BatchTrace, error)
	RecordTraces(traces []*model.BatchTrace) error
	GetTraceByBatch(batchID int64) ([]model.BatchTrace, error)
	// partial=true 表示按包含关系匹配；入口的「匹配方式」下拉传它。
	TraceByProduct(batchNo string, partial bool) ([]model.BatchTrace, error)
	TraceByPart(value string, partial bool) ([]model.BatchTrace, error)
	// TraceByPartLot 按零件入库批次号反查用了它的生产批次。
	TraceByPartLot(batchNo string, partial bool) ([]model.BatchTrace, error)
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
// 恢复与清空都是「覆盖」而非「追加」，且都会先自动留一份可回退的副本（#26）。
type Backup interface {
	DataDir() string
	SetDataDir(dir string) error
	BackupDatabase(saveDir string) (string, error)
	RestoreDatabase(filePath string) (RestoreResult, error)
	ClearDatabase() (ClearResult, error)
	ExportAuditLogCSV(startDate, endDate time.Time, filePath string) (int, error)
	ExportAllDataCSV(saveDir string) (map[string]string, string, error)
}

// RestoreResult 一次整库恢复的结果（#26）。
type RestoreResult struct {
	RequiresRestart bool `json:"requiresRestart,omitempty"`
	// Statements 实际执行的 INSERT 语句数。
	Statements int `json:"statements"`
	// PreRestore 恢复前自动生成的完整副本路径，可用于回退。
	PreRestore string `json:"preRestore,omitempty"`
	// TableCounts 恢复后各业务表的行数，供用户核对。
	TableCounts map[string]int `json:"tableCounts,omitempty"`
}

// ClearResult 一次清空数据的结果（#26）。
type ClearResult struct {
	// PreClear 清空前自动生成的完整副本路径，可用于回退。
	PreClear string `json:"preClear,omitempty"`
	// Cleared 被清空的表名。
	Cleared []string `json:"cleared,omitempty"`
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
	ServerDescriptor
}
