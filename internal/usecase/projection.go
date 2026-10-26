package usecase

// 库存投影：把「未完成批次将要消耗的量」从库存里先扣掉，得到完成后的余量。
//
// 它与「当前库存」是两个时间维度：当前库存回答"现在有没有货"，
// 投影回答"这些在产批次做完之后会不会缺"。两者都需要，且不能互相替代。
//
// 计算口径必须与完成生产时真正扣料的那套完全一致（同一个 bomConsume、
// 同样按 plan_qty、同样排除跳过零件）——否则提示会说"够"而完成被拒。

// ProjectionHolder 是一条占用来源：哪个批次、占了多少。
type ProjectionHolder struct {
	BatchNo string  `json:"batch_no"`
	Qty     float64 `json:"qty"`
	// Planned 标记"假设中的一批"：建批弹窗预览时把用户正在填的计划也算进去。
	Planned bool `json:"planned,omitempty"`
}

// PartProjection 是单个零件的投影结果。
type PartProjection struct {
	PartID int64 `json:"part_id"`
	// Stock / Warn 随行返回，方便调用方自己算档位，也避免零件列表与投影
	// 来自两次不同请求时的拼接错位。
	Stock float64 `json:"stock"`
	Warn  float64 `json:"warn"`
	// Committed 是所有未完成批次对它的消耗之和（尚未从库存扣除）。
	Committed float64 `json:"committed"`
	// After 是全部在产批次完成后的余量（stock - committed）。
	After   float64            `json:"after"`
	Holders []ProjectionHolder `json:"holders,omitempty"`
}

// StockProjectionInput 可选地把"一批还没创建的计划"并入计算。
// 两个字段必须同时给出；只给一个时 handler 会拒绝。
type StockProjectionInput struct {
	ProductID int64
	PlanQty   int
}

// PartReserveBrief 是某个批次里"导致备料不足/将预警"的零件摘要。
type PartReserveBrief struct {
	PartID int64   `json:"part_id"`
	Code   string  `json:"code"`
	Name   string  `json:"name"`
	After  float64 `json:"after"`
	Warn   float64 `json:"warn"`
}

// BatchReserve 是按批次汇总的备料结论，供批次列表角标与工作台「生产计划」
// 直接使用——前端不必为每个批次再拉一遍 BOM 与跳过清单。
//
// Level 取该批所用零件的最坏档（口径是"全部在产批次都完成"）：
//   - short: 有零件完成后为负（做不出来）
//   - low:   有零件完成后低于预警线（做得完但要补货）
//   - ok:    其余
type BatchReserve struct {
	BatchID int64              `json:"batch_id"`
	Level   string             `json:"level"`
	Parts   []PartReserveBrief `json:"parts,omitempty"`
}

// StockProjectionSummary 是给工作台与卡片用的计数，只统计启用中的零件。
type StockProjectionSummary struct {
	WillWarn  int `json:"willWarn"`
	WillShort int `json:"willShort"`
}

type StockProjectionResult struct {
	Parts   []PartProjection       `json:"parts"`
	Batches []BatchReserve         `json:"batches"`
	Summary StockProjectionSummary `json:"summary"`
}
