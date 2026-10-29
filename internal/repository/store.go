package repository

import (
	"database/sql"
	"time"

	"app/internal/model"
)

// SkipPartRow is one batch-skip-part edge. Field db tags are part of the
// Store contract ABI — adapters must not reshape them casually.
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

	// CountRows 读取指定表的行数。表名来自代码内固定清单，不接受外部输入。
	CountRows(table string, dest *int) error

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
	// AddPartInbound 记一笔入库并生成批次号（先插行后按 id 回填，同事务内完成）。
	AddPartInbound(partID int64, qty float64, note *string) (string, error)
	// ListPartInbounds 按时间倒序列出零件的入库批次（详情页用）。
	ListPartInbounds(partID int64) ([]model.PartInbound, error)
	// GetInbound 读一笔入库批次（逐批次盘点前核对归属与旧值）。
	GetInbound(inboundID int64) (*model.PartInbound, error)
	// SetInboundRemaining 把某批次剩余设为指定值。
	SetInboundRemaining(inboundID int64, remaining float64) error
	// DrawPartInboundFIFO 按先进先出从零件的入库批次扣减，并记扣料明细。
	DrawPartInboundFIFO(partID, batchID int64, qty float64) error
	// ReducePartInboundFIFO 只扣批次剩余、不记扣料明细（盘点调减）。
	ReducePartInboundFIFO(partID int64, qty float64) error
	// RestorePartInboundDraws 把某生产批次扣掉的量还回原批次，返回还回总量。
	RestorePartInboundDraws(batchID int64) (float64, error)
	// BatchInboundDraws 某生产批次在某零件上已扣的批次量（对账用）。
	BatchInboundDraws(batchID, partID int64) (float64, error)

	GetBOMByProduct(productID int64) ([]model.BOMItem, error)
	CreateBOMItem(b *model.BOMItem) (int64, error)
	// GetBOMItemForUpdate 读一行 BOM 明细并加行锁，用于更新前的存在性与审计基线。
	GetBOMItemForUpdate(id int64) (*model.BOMItem, error)
	// UpdateBOMItem 只改用量、用量模式、损耗率与可替换标记；version 用作乐观锁。
	UpdateBOMItem(b *model.BOMItem) (int64, error)
	DeleteBOMItem(id int64) error
	// BOMProductsForPart 列出 BOM 里引用了该零件的产品，用于删除前的拒绝提示。
	BOMProductsForPart(partID int64) ([]model.ProductRef, error)
	// TracesForPart 按批次汇总该零件的生产投料记录，用于删除前的拒绝提示。
	TracesForPart(partID int64) ([]model.PartTraceRef, error)
	// BatchUsesForPart 列出临时零件/跳过零件/完成扣料对它的引用，用于删除守卫。
	BatchUsesForPart(partID int64) ([]model.PartBatchUse, error)

	GetBatchForUpdate(id int64) (*model.ProductBatch, error)
	CreateBatch(b *model.ProductBatch) (int64, error)
	UpdateBatchProduced(id int64, qty int) error
	MarkBatchConsumptionRecorded(id int64) error
	UpdateBatchStatusFrom(id int64, status int, operator string, fromStatus int) (int64, error)

	CreateAuditLog(log *model.AuditLog) error
	CreateBatchConsumption(c *model.BatchConsumption) error
	CreateTrace(trace *model.BatchTrace) (int64, error)
	// ReplacePartTrace 整体替换 (batch, part) 的投料记录与批次分配（记录投料是覆盖）。
	ReplacePartTrace(trace *model.BatchTrace) (int64, error)
	// DrawPartInboundForBatch 按该批次投料分配的顺序扣入库批次，余量走先进先出。
	DrawPartInboundForBatch(partID, batchID int64, qty float64) error
	// DeletePartTrace 删掉 (batch, part) 的投料记录与分配，返回删掉的记录条数
	// （标记跳过时自动清理）。
	DeletePartTrace(batchID, partID int64) (int64, error)
	ListBatchConsumptions(batchID int64) ([]model.BatchConsumption, error)
	GetSkippedParts(batchID int64) ([]int64, error)
	AddSkipPart(batchID, partID int64) error
	RemoveSkipPart(batchID, partID int64) error
	GetTempParts(batchID int64) ([]model.BatchTempPart, error)
	GetAllTempParts() ([]model.BatchTempPart, error)
	AddTempPart(tp *model.BatchTempPart) error
	RemoveTempPart(batchID, partID int64) error
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
	// BOMProductsForPart 列出 BOM 里引用了该零件的产品（读侧；删除守卫在 TxOps 上走同一个查询）。
	BOMProductsForPart(partID int64) ([]model.ProductRef, error)
	// TracesForPart 按批次汇总该零件的生产投料记录（读侧；删除守卫在 TxOps 上走同一个查询）。
	TracesForPart(partID int64) ([]model.PartTraceRef, error)
	// ListPartInbounds 零件的入库批次列表（读侧；写入只发生在 StockIn 事务里）。
	ListPartInbounds(partID int64) ([]model.PartInbound, error)
	// ListBatchTraceAllocs 某生产批次按零件分组的投料批次分配（读侧）。
	ListBatchTraceAllocs(batchID int64) (map[int64][]model.BatchTraceAlloc, error)
	// InboundOccupancy 每个入库批次被未完成批次预定了多少（读侧）。
	InboundOccupancy(partID int64) (map[int64][]model.PartInboundAlloc, error)

	ListBatches() ([]model.ProductBatch, error)
	GetSkippedParts(batchID int64) ([]int64, error)
	GetAllSkippedParts() ([]SkipPartRow, error)
	GetTempParts(batchID int64) ([]model.BatchTempPart, error)
	GetAllTempParts() ([]model.BatchTempPart, error)
	CreateTrace(t *model.BatchTrace) (int64, error)
	GetTraceByBatch(batchID int64) ([]model.BatchTrace, error)
	TraceByProduct(batchNo string) ([]model.BatchTrace, error)
	TraceByPart(partCode string) ([]model.BatchTrace, error)

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

	// Database identity / schema version（D-016 接入服务器前期准备）。
	// GetDatabaseID 返回 db_identity 里的稳定标识；缺失时返回空串而非报错。
	GetDatabaseID() (string, error)
	// GetSchemaVersion 返回已应用的最高迁移版本。
	GetSchemaVersion() (int, error)
}

var (
	_ Store = (*Repository)(nil)
	_ TxOps = (*Tx)(nil)
	_ Store = (*SQLiteStore)(nil)
	_ TxOps = (*SQLiteTx)(nil)
)
