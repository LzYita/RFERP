package model

import "time"

type Product struct {
	ID        int64     `db:"id" json:"id"`
	Code      string    `db:"code" json:"code"`
	Name      string    `db:"name" json:"name"`
	Spec      *string   `db:"spec" json:"spec,omitempty"`
	Unit      string    `db:"unit" json:"unit"`
	Status    int       `db:"status" json:"status"`
	Version   int       `db:"version" json:"version"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
	Operator  *string   `db:"operator" json:"operator,omitempty"`
}

type Part struct {
	ID        int64     `db:"id" json:"id"`
	Code      string    `db:"code" json:"code"`
	Name      string    `db:"name" json:"name"`
	Spec      *string   `db:"spec" json:"spec,omitempty"`
	Unit      string    `db:"unit" json:"unit"`
	PartType  *string   `db:"part_type" json:"part_type,omitempty"`
	StockQty  float64   `db:"stock_qty" json:"stock_qty"`
	WarnQty   float64   `db:"warn_qty" json:"warn_qty"`
	Status    int       `db:"status" json:"status"`
	Version   int       `db:"version" json:"version"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
	Operator  *string   `db:"operator" json:"operator,omitempty"`
	Supplier  *string   `db:"supplier" json:"supplier,omitempty"`
}

type BOMItem struct {
	ID          int64     `db:"id" json:"id"`
	ProductID   int64     `db:"product_id" json:"product_id"`
	PartID      int64     `db:"part_id" json:"part_id"`
	Quantity    float64   `db:"quantity" json:"quantity"`
	LossRate    float64   `db:"loss_rate" json:"loss_rate"`
	Remark      *string   `db:"remark" json:"remark,omitempty"`
	Version     int       `db:"version" json:"version"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
	Operator    *string   `db:"operator" json:"operator,omitempty"`
	Replaceable int       `db:"replaceable" json:"replaceable"`
	// UseMode 用量模式: 0=1台产品用N个零件, 1=M台产品用1个零件(包装箱, 消耗按ceil向上取整)
	UseMode int `db:"use_mode" json:"use_mode"`
	// 关联查询填充
	PartCode *string `db:"part_code" json:"part_code,omitempty"`
	PartName *string `db:"part_name" json:"part_name,omitempty"`
}

type ProductBatch struct {
	ID          int64     `db:"id" json:"id"`
	BatchNo     string    `db:"batch_no" json:"batch_no"`
	ProductID   int64     `db:"product_id" json:"product_id"`
	PlanQty     int       `db:"plan_qty" json:"plan_qty"`
	ProducedQty int       `db:"produced_qty" json:"produced_qty"`
	Status      int       `db:"status" json:"status"`
	Version     int       `db:"version" json:"version"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
	Operator    *string   `db:"operator" json:"operator,omitempty"`
	Customer    *string   `db:"customer" json:"customer,omitempty"`
	// 关联
	ProductName *string `db:"product_name" json:"product_name,omitempty"`
	ProductCode *string `db:"product_code" json:"product_code,omitempty"`
}

type BatchTrace struct {
	ID          int64     `db:"id" json:"id"`
	BatchID     int64     `db:"batch_id" json:"batch_id"`
	PartID      int64     `db:"part_id" json:"part_id"`
	PartBatchNo *string   `db:"part_batch_no" json:"part_batch_no,omitempty"`
	UsedQty     float64   `db:"used_qty" json:"used_qty"`
	Supplier    *string   `db:"supplier" json:"supplier,omitempty"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
	Operator    *string   `db:"operator" json:"operator,omitempty"`
}

type AuditLog struct {
	ID        int64           `db:"id" json:"id"`
	TableName string          `db:"table_name" json:"table_name"`
	RecordID  int64           `db:"record_id" json:"record_id"`
	Action    string          `db:"action" json:"action"`
	OldData   *map[string]any `db:"old_data" json:"old_data,omitempty"`
	NewData   *map[string]any `db:"new_data" json:"new_data,omitempty"`
	Operator  *string         `db:"operator" json:"operator,omitempty"`
	CreatedAt time.Time       `db:"created_at" json:"created_at"`
}
