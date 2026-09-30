package service

import (
	"errors"

	"app/internal/repository"
	"app/internal/usecase"
)

// bulkLoadOutcome carries the repository's commit boundary to UI callers.
// Ordinary pre-commit failures remain ordinary errors (with a confirmed rollback).
func bulkLoadOutcome(err error) error {
	var outcome *repository.BulkLoadError
	if !errors.As(err, &outcome) {
		return err
	}
	state := usecase.OperationUnknown
	if outcome.Outcome == repository.BulkLoadApplied {
		state = usecase.OperationApplied
	}
	return &usecase.OperationError{State: state, Err: err}
}
