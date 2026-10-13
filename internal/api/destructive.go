package api

import (
	"errors"
	"log"
	"net/http"
	"path/filepath"

	"app/internal/usecase"
)

// 破坏性操作的三分类结果。
//
// service 层已经把这件事说清楚了（usecase/operation.go）：普通 error = 事务已回滚、
// 数据未应用；*OperationError{Unknown} = 提交或回滚结果不明；{Applied} = 已提交但
// 收尾失败。**但此前 API 层只用 writeErr 把它拍扁成一段中文 + 500**，前端拿到的
// 只有 error 字符串，无法程序化区分"可以改一改再来"和"千万别重试"。
//
// 而这三种情况的正确处置完全相反：
//
//	rolled_back → 补料 / 换文件后可以重试
//	unknown     → 先核对数据，绝不能重试
//	applied     → 数据已经变了，重试等于做两遍
//
// 计划 §Round 5 要求「结果不确定」用黄色、主文案写「不要重试」、且**不渲染重试按钮**
// —— 这三条都要靠这里的 state 字段才做得出来，所以它不是锦上添花，是前提。
const (
	// OpStateRolledBack：已确认回滚，未应用。可修正原因后重试。
	OpStateRolledBack = "rolled_back"
	// OpStateUnknown：提交/回滚结果不明，可能已部分应用。**不得重试。**
	OpStateUnknown = "unknown"
	// OpStateApplied：已提交，仅收尾失败。重试会造成二次操作。
	OpStateApplied = "applied"
)

// destructiveState 从错误里取出三分类。
//
// 缺省是 rolled_back 而不是 unknown：**普通 error 在 service 侧的语义就是
// "已确认回滚"**（bulkLoadOutcome 只在 COMMIT/ROLLBACK 本身失败时才包装成
// OperationError）。把缺省判成 unknown 会让绝大多数可重试的失败都被标成"别重试"。
func destructiveState(err error) string {
	var oe *usecase.OperationError
	if !errors.As(err, &oe) {
		return OpStateRolledBack
	}
	if oe.State == usecase.OperationApplied {
		return OpStateApplied
	}
	return OpStateUnknown
}

// destructiveStatus 把三分类映射成 HTTP 状态码。
//
// rolled_back 用 400 而不是 500：它意味着"请求本身不成立"（文件无效、库存不足、
// 口令不对），归为服务端故障会误导用户和监控。unknown / applied 都用 500 —— 它们
// 确实是需要人工介入的异常，但**必须配合 state 字段**才有意义，光看状态码区分不了。
func destructiveStatus(state string) int {
	if state == OpStateRolledBack {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// writeDestructiveErr 是破坏性操作失败时唯一的出口。
//
// 原始错误只进日志，不回给客户端：它可能是 SQL 错误，里面有表名与语句结构。
// 客户端拿到的 error 文本按三分类给固定的、可执行的中文提示。
func writeDestructiveErr(w http.ResponseWriter, op string, err error) {
	state := destructiveState(err)
	log.Printf("api: %s failed (state=%s): %v", op, state, err)
	writeJSON(w, destructiveStatus(state), map[string]string{
		"error": destructiveMessage(state),
		"state": state,
	})
}

// destructiveMessage 是三分类各自的中文主文案。
//
// 文案写死在这里而不是用 err.Error()，有两个原因：一是 OperationError 的文案
// （usecase/operation.go:20-27）只区分 applied/unknown 且都要求"不要直接重试"，
// 对"已回滚"是错的误导；二是把内部错误原文挡在客户端之外。
func destructiveMessage(state string) string {
	switch state {
	case OpStateApplied:
		return "数据变更已提交，但后续处理失败。请先核对数据与操作前副本，不要直接重试"
	case OpStateUnknown:
		return "事务提交或回滚结果不明，数据可能已部分应用。请先核对数据与操作前副本，不要重试"
	default:
		return "操作未生效，数据已回滚到操作前状态"
	}
}

// safetyCopyName 只回文件名，不回绝对路径。
//
// 副本路径形如 D:\仓库数据\备份\pre_restore_20260929_110000.sql，回给客户端等于
// 泄露 Windows 用户名与目录布局（§4.5 缺口 5 记的正是这类问题）。用户要退回时
// 从备份文件列表里按文件名选即可，列表端点同样只给文件名。
func safetyCopyName(path string) string {
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}
