package usecase

import "fmt"

// OperationState describes the data state when a destructive operation returns an error.
type OperationState string

const (
	OperationApplied OperationState = "applied"
	OperationUnknown OperationState = "unknown"
)

// OperationError preserves the cause while distinguishing a post-commit failure
// from an uncertain transaction outcome. Neither state is safe to blindly retry.
type OperationError struct {
	State OperationState
	Err   error
}

func (e *OperationError) Error() string {
	switch e.State {
	case OperationApplied:
		return fmt.Sprintf("数据变更已提交，但后续处理失败：%v。请先核对数据和操作前副本，不要直接重试", e.Err)
	default:
		return fmt.Sprintf("事务提交或回滚结果不明：%v。请先核对数据和操作前副本，不要直接重试", e.Err)
	}
}

func (e *OperationError) Unwrap() error { return e.Err }
