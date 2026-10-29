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

// ProductRef 是「删除零件被拒」时回显给界面的最小产品信息。
// 只带 id/编码/名称：界面要把「这个零件被哪些产品用了」说清楚，多带字段
// 只会让响应变长，也让调用方误以为这里是完整的产品对象。
type ProductRef struct {
	ID   int64  `db:"id" json:"id"`
	Code string `db:"code" json:"code"`
	Name string `db:"name" json:"name"`
}

// PartTraceRef 是「删除零件被拒」时按批次汇总的投料记录数量。
// 追溯记录是生产历史，不能按 BOM 那样让用户去删；这里只告诉他
// 「哪些批次用过它、各用了多少条」，便于判断该怎么处理（通常是停用零件）。
type PartTraceRef struct {
	BatchNo string `db:"batch_no" json:"batch_no"`
	Records int    `db:"records" json:"records"`
}

// PartBatchUse 是删除零件时的一条"仍被使用"引用：哪个批次、哪一类用途。
// Reason 取 temp（临时零件）/ skip（跳过零件）/ consumed（完成扣料）。
type PartBatchUse struct {
	BatchNo string `db:"batch_no" json:"batch_no"`
	Reason  string `db:"reason" json:"reason"`
}

// PartInbound 是零件的一笔入库：自动生成的批次号、入库量、剩余量与备注。
//
// 库存总量仍是 parts.stock_qty，恒等于该零件全部批次 Remaining 之和：
// 生产扣料按先进先出扣批次，逐批次盘点直接改 Remaining。Remaining 为 0
// 的批次称为"已用完"，零件详情的一级界面只列还有剩余的批次。
//
// Occupied / OccupiedBy 只在零件详情查询里填充：未完成批次通过投料计划
// 已经「预定」了多少。它不代表库存已经减少（那是完成生产时才发生的），
// 用来回答"这批剩余里有多少是别人等着用的"。
type PartInbound struct {
	ID         int64              `db:"id" json:"id"`
	PartID     int64              `db:"part_id" json:"part_id"`
	BatchNo    string             `db:"batch_no" json:"batch_no"`
	Qty        float64            `db:"qty" json:"qty"`
	Remaining  float64            `db:"remaining" json:"remaining"`
	Note       *string            `db:"note" json:"note,omitempty"`
	CreatedAt  time.Time          `db:"created_at" json:"created_at"`
	Occupied   float64            `db:"-" json:"occupied"`
	OccupiedBy []PartInboundAlloc `db:"-" json:"occupied_by,omitempty"`
}

// PartInboundAlloc 是一笔"未完成批次预定了这个入库批次多少"，供零件详情展开。
type PartInboundAlloc struct {
	BatchNo string  `db:"batch_no" json:"batch_no"`
	Qty     float64 `db:"qty" json:"qty"`
}

// PartInboundDraw 是一笔"生产批次从某入库批次扣了多少"的明细。
// 撤销批次按它把数量还回原批次，而不是笼统加回总量。
type PartInboundDraw struct {
	ID        int64     `db:"id" json:"id"`
	BatchID   int64     `db:"batch_id" json:"batch_id"`
	InboundID int64     `db:"inbound_id" json:"inbound_id"`
	Qty       float64   `db:"qty" json:"qty"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
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
	ID                  int64     `db:"id" json:"id"`
	BatchNo             string    `db:"batch_no" json:"batch_no"`
	ProductID           int64     `db:"product_id" json:"product_id"`
	PlanQty             int       `db:"plan_qty" json:"plan_qty"`
	ProducedQty         int       `db:"produced_qty" json:"produced_qty"`
	Status              int       `db:"status" json:"status"`
	Version             int       `db:"version" json:"version"`
	CreatedAt           time.Time `db:"created_at" json:"created_at"`
	UpdatedAt           time.Time `db:"updated_at" json:"updated_at"`
	Operator            *string   `db:"operator" json:"operator,omitempty"`
	Customer            *string   `db:"customer" json:"customer,omitempty"`
	ConsumptionRecorded int       `db:"consumption_recorded" json:"consumption_recorded"`
	// 关联
	ProductName *string `db:"product_name" json:"product_name,omitempty"`
	ProductCode *string `db:"product_code" json:"product_code,omitempty"`
}

// BatchConsumption records the actual quantity removed from one part when a
// batch is completed. It is immutable after completion and is the source of
// truth for a later batch reversal.
type BatchConsumption struct {
	BatchID     int64     `db:"batch_id" json:"batch_id"`
	PartID      int64     `db:"part_id" json:"part_id"`
	ConsumedQty float64   `db:"consumed_qty" json:"consumed_qty"`
	CreatedAt   time.Time `db:"created_at" json:"created_at"`
}

// BatchTempPart 是批次上的临时零件：不在产品 BOM 里、但这一批实际要用料。
// Qty 是**本批总量**（不是每台用量）：临时 addition 是一次性的，套用
// use_mode/损耗率只会让填写的人猜不透口径。完成生产时按 Qty 整笔扣减，
// 与 BOM 零件走同一套缺料拒绝与撤销回退。
type BatchTempPart struct {
	BatchID   int64     `db:"batch_id" json:"batch_id"`
	PartID    int64     `db:"part_id" json:"part_id"`
	Qty       float64   `db:"qty" json:"qty"`
	Code      string    `db:"code" json:"code"`
	Name      string    `db:"name" json:"name"`
	Unit      string    `db:"unit" json:"unit"`
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	// 推导依据（可空）：表单按 BOM 行给出使用方式/单位用量/损耗率并自动算
	// 总量时记录下来，手填总量时为 NULL。扣减永远以 Qty 为准。
	UseMode  *int     `db:"use_mode" json:"use_mode,omitempty"`
	Quantity *float64 `db:"quantity" json:"quantity,omitempty"`
	LossRate *float64 `db:"loss_rate" json:"loss_rate,omitempty"`
}

// BatchTrace 是一个生产批次对一个零件的投料记录。
//
// v18 起一条记录代表「本批这个零件一共用了多少」，Allocations 说明这些用量
// 从哪几个入库批次出、按什么顺序扣（见 BatchTraceAlloc）。同一批次的同一
// 零件只有一条记录：再次记录投料是覆盖，不是追加。PartBatchNo 是 v18 之前
// 的自由文本批次号，保留仅供历史记录展示。
type BatchTrace struct {
	ID          int64             `db:"id" json:"id"`
	BatchID     int64             `db:"batch_id" json:"batch_id"`
	PartID      int64             `db:"part_id" json:"part_id"`
	PartBatchNo *string           `db:"part_batch_no" json:"part_batch_no,omitempty"`
	UsedQty     float64           `db:"used_qty" json:"used_qty"`
	Supplier    *string           `db:"supplier" json:"supplier,omitempty"`
	CreatedAt   time.Time         `db:"created_at" json:"created_at"`
	Operator    *string           `db:"operator" json:"operator,omitempty"`
	Allocations []BatchTraceAlloc `db:"-" json:"allocations,omitempty"`
}

// BatchTraceAlloc 是一条投料分配：本批从哪一收入库批次扣、扣多少、第几顺位。
// Position 从 0 起，完成生产时按它从小到大扣批次。
type BatchTraceAlloc struct {
	InboundID int64   `db:"inbound_id" json:"inbound_id"`
	BatchNo   string  `db:"batch_no" json:"batch_no"`
	Qty       float64 `db:"qty" json:"qty"`
	Position  int     `db:"position" json:"position"`
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

type User struct {
	ID           int64      `db:"id" json:"id"`
	Username     string     `db:"username" json:"username"`
	PasswordHash string     `db:"password_hash" json:"-"`
	DisplayName  *string    `db:"display_name" json:"display_name,omitempty"`
	Role         string     `db:"role" json:"role"`
	Status       int        `db:"status" json:"status"`
	LastLoginAt  *time.Time `db:"last_login_at" json:"last_login_at,omitempty"`
	CreatedAt    time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at" json:"updated_at"`
}
