package runtime

import "fmt"

// ProjectionError means SQL committed but the in-memory world cannot be trusted.
// The owner stops processing and the application must reconstruct from storage.
type ProjectionError struct {
	Cause error
}

func (e *ProjectionError) Error() string {
	return fmt.Sprintf("committed projection failed: %v", e.Cause)
}

func (e *ProjectionError) Unwrap() error {
	return e.Cause
}

func (e *Engine) Done() <-chan struct{} {
	return e.done
}
