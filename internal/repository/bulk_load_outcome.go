package repository

// BulkLoadOutcome is the state of the business transaction when bulk loading
// returns an error. An unknown outcome must not be described as a rollback.
type BulkLoadOutcome string

const (
	BulkLoadApplied BulkLoadOutcome = "applied"
	BulkLoadUnknown BulkLoadOutcome = "unknown"
)

// BulkLoadError annotates errors at or after the commit boundary.
type BulkLoadError struct {
	Outcome BulkLoadOutcome
	Err     error
}

func (e *BulkLoadError) Error() string { return e.Err.Error() }
func (e *BulkLoadError) Unwrap() error { return e.Err }
