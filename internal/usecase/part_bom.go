package usecase

import (
	"fmt"

	"app/internal/model"
)

// PartInBOMError 表示删除零件被拒：它仍被这些产品的 BOM 引用。
//
// 为什么单独一个错误类型而不是返回字符串：界面要把产品编码和名称**直接列出来**
// （「它被哪些产品用了」），只把这句话塞进 error 文案里，前端就只能正则去抠。
// API 层用 errors.As 认出它并回 409 + products 数组。
type PartInBOMError struct {
	PartCode string
	Products []model.ProductRef
}

func (e *PartInBOMError) Error() string {
	switch len(e.Products) {
	case 0:
		return fmt.Sprintf("零件 %s 仍被 BOM 引用，无法删除", e.PartCode)
	case 1:
		p := e.Products[0]
		return fmt.Sprintf("零件 %s 已被产品 %s（%s）的 BOM 引用，无法删除", e.PartCode, p.Code, p.Name)
	default:
		return fmt.Sprintf("零件 %s 已被 %d 个产品的 BOM 引用，无法删除", e.PartCode, len(e.Products))
	}
}
