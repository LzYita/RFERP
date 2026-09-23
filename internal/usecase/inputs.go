package usecase

// 写操作入参：传输无关，便于 HTTP API 直接映射（Phase B1）。
// Operator 由应用上下文/会话注入；未来 API 不得信任客户端随意指定。

// StockInInput 零件入库。
type StockInInput struct {
	PartID   int64
	Qty      float64
	Operator string
}

// AdjustStockInput 库存盘点/调整到指定数量。
type AdjustStockInput struct {
	PartID   int64
	NewQty   float64
	Operator string
}

// UpdateBatchStatusInput 批次状态流转（不含撤销；撤销用 RevokeBatchInput）。
type UpdateBatchStatusInput struct {
	ID       int64
	Status   int
	Operator string
}

// RevokeBatchInput 撤销批次。
type RevokeBatchInput struct {
	ID       int64
	Operator string
}

// CreateUserInput 创建用户。
type CreateUserInput struct {
	Username    string
	Password    string
	DisplayName string
	Role        string
}

// UpdateUserInput 更新用户资料/角色/状态。
type UpdateUserInput struct {
	ID          int64
	DisplayName string
	Role        string
	Status      int
}

// ResetPasswordInput 管理员重置密码。
type ResetPasswordInput struct {
	ID       int64
	Password string
}

// ChangePasswordInput 用户修改自己的密码。
type ChangePasswordInput struct {
	ID      int64
	OldPass string
	NewPass string
}

// DeleteUserInput 删除用户。
type DeleteUserInput struct {
	ID int64
}
