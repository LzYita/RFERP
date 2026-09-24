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
