package repository

import (
	"errors"
	"fmt"
	"strings"
)

// Stable business errors (PLAN §1.1 / D-013): Service and API branch on these,
// adapters translate driver-specific failures into them.
var (
	// ErrUniqueConflict is returned when a unique constraint rejects a write.
	ErrUniqueConflict = errors.New("唯一键冲突")
	// ErrOptimisticLock is returned when a versioned update matches zero rows.
	ErrOptimisticLock = errors.New("并发版本冲突，请刷新后重试")
)

// uniqueConstraintMessage maps a table.column pair to a message a user can act
// on.
//
// 之前是把驱动原文直接拼在后面：用户看到的是
//
//	新增失败: 唯一键冲突: UNIQUE constraint failed: parts.code
//
// 英文驱动串对用户毫无意义，而这是 Web UI 里第一次真正把这些错误显示出来
// （Fyne 版靠弹窗，Web 版要放进表单的错误提示里）。抽不出已知约束时才退回
// 原文 —— 那时至少信息没有丢失。
var uniqueConstraintMessage = map[string]string{
	"parts.code":    "零件编码已存在",
	"products.code": "产品编码已存在",
	"bom_items.product_id, bom_items.part_id": "该产品BOM中已包含此零件",
}

// translateUniqueConstraint 从驱动错误里认出被违反的约束，返回 table.column。
//
// SQLite: UNIQUE constraint failed: parts.code
// MySQL : Duplicate entry 'P-001' for key 'parts.code'
//
// 只返回列名，不做文案映射 —— 映射在调用处做一次，避免"返回的到底是列名还是
// 文案"这种歧义（第一版就栽在这里）。
func translateUniqueConstraint(msg string) string {
	for _, marker := range []string{"unique constraint failed:", "for key '", "for key "} {
		idx := strings.Index(msg, marker)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(msg[idx+len(marker):])
		// 只有 MySQL 的 for key 形式才在逗号后是别的东西。SQLite 的多列约束
		// （bom_items.product_id, bom_items.part_id）逗号本身是键的一部分。
		if !strings.HasPrefix(marker, "unique constraint") {
			if i := strings.Index(rest, ","); i >= 0 {
				rest = rest[:i]
			}
		}
		rest = strings.Trim(strings.TrimSpace(rest), "'`")
		if i := strings.Index(rest, "'"); i >= 0 {
			rest = rest[:i]
		}
		return strings.TrimSpace(rest)
	}
	return ""
}

// TranslateError maps driver errors to stable repository errors.
// Unrecognized errors are wrapped so callers can still inspect them.
func TranslateError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrUniqueConflict) || errors.Is(err, ErrOptimisticLock) {
		return err
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "unique constraint failed"), // SQLite
		strings.Contains(msg, "duplicate entry"), // MySQL 1062
		strings.Contains(msg, "constraint failed") && strings.Contains(msg, "unique"):
		if col := translateUniqueConstraint(msg); col != "" {
			if pretty, ok := uniqueConstraintMessage[col]; ok {
				return fmt.Errorf("%w: %s", ErrUniqueConflict, pretty)
			}
		}
		return fmt.Errorf("%w: %v", ErrUniqueConflict, err)
	default:
		return err
	}
}

// OptimisticLockError is the explicit form used when RowsAffected==0 on a
// versioned update.
func OptimisticLockError(err error) error {
	if err == nil {
		return ErrOptimisticLock
	}
	return fmt.Errorf("%w: %v", ErrOptimisticLock, err)
}
