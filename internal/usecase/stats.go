package usecase

// 统计结果类型由用例层持有，便于未来 API 直接映射；
// internal/service 使用类型别名保持现有 UI 编译兼容。

// StockDailyPoint 某日进出汇总
type StockDailyPoint struct {
	Date string  // MM-DD
	In   float64 // 入库量
	Out  float64 // 出库量
}

// PartStockStat 单个零件近期的进出库统计
type PartStockStat struct {
	Code string
	Name string
	In   float64
	Out  float64
}

// SupplierStockStat 供应商入库统计（入库主要针对零件）
type SupplierStockStat struct {
	Name string
	In   float64
}

// CustomerStockStat 客户出库统计（出库关联客户，产品按批次）
type CustomerStockStat struct {
	Name string
	Out  float64
}

// ProductStockStat 产品出库统计
type ProductStockStat struct {
	Code string
	Name string
	Out  float64
}

// StockStats 近期进出库统计结果
type StockStats struct {
	Days            []StockDailyPoint
	TopIn           []PartStockStat     // 零件入库量前10
	TopOut          []PartStockStat     // 零件出库量前10
	TopSuppliers    []SupplierStockStat // 供应商入库前10
	TopCustomers    []CustomerStockStat // 客户出库前10
	TopProducts     []ProductStockStat  // 产品出库前10
	TotalIn         float64
	TotalOut        float64
	ProductOutTotal float64
}
