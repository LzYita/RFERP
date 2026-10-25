package usecase

import (
	"fmt"
	"strings"

	"app/internal/model"
)

// PartInTraceError 表示删除零件被拒：它仍被生产投料（追溯）记录引用。
//
// 和 PartInBOMError 分开：BOM 引用是**用户可以自己解开的**（去 BOM 管理删条目），
// 追溯记录是生产历史，解除不了——所以界面给的建议也不同（改「停用」而不是去删记录）。
type PartInTraceError struct {
	PartCode string
	Batches  []model.PartTraceRef
}

// Records 是被引用的投料记录总条数。
func (e *PartInTraceError) Records() int {
	total := 0
	for _, b := range e.Batches {
		total += b.Records
	}
	return total
}

func (e *PartInTraceError) Error() string {
	if len(e.Batches) == 0 {
		return fmt.Sprintf("零件 %s 仍有生产投料记录，无法删除", e.PartCode)
	}
	// 只点名前几个批次，避免一条错误文案长到把对话框撑开；完整清单走 JSON。
	const preview = 3
	names := make([]string, 0, preview)
	for i, b := range e.Batches {
		if i == preview {
			names = append(names, "等")
			break
		}
		names = append(names, b.BatchNo)
	}
	return fmt.Sprintf(
		"零件 %s 有 %d 条生产投料记录（涉及 %d 个批次：%s），追溯记录不能删除",
		e.PartCode, e.Records(), len(e.Batches), strings.Join(names, "、"),
	)
}
