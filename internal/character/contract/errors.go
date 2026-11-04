package contract

import "errors"

var (
	ErrInvalidInput      = errors.New("invalid character input")
	ErrInvalidConfig     = errors.New("invalid character configuration")
	ErrNotFound          = errors.New("character not found")
	ErrIncapacitated     = errors.New("character incapacitated")
	ErrOperationConflict = errors.New("character operation conflict")
	ErrExhausted         = errors.New("character numeric or time range exhausted")
	ErrStorage           = errors.New("character storage operation failed")
)

// ErrorKind is an inspectable domain failure category.
type ErrorKind string

const (
	ErrorInvalidInput      ErrorKind = "invalid_input"
	ErrorInvalidConfig     ErrorKind = "invalid_config"
	ErrorNotFound          ErrorKind = "not_found"
	ErrorIncapacitated     ErrorKind = "incapacitated"
	ErrorOperationConflict ErrorKind = "operation_conflict"
	ErrorExhausted         ErrorKind = "exhausted"
)

// DomainError reports an expected failure. Field identifies the affected input
// for programmatic inspection. Error never interpolates supplied field values.
type DomainError struct {
	Kind  ErrorKind
	Field string
}

func (e *DomainError) category() error {
	switch e.Kind {
	case ErrorInvalidInput:
		return ErrInvalidInput
	case ErrorInvalidConfig:
		return ErrInvalidConfig
	case ErrorNotFound:
		return ErrNotFound
	case ErrorIncapacitated:
		return ErrIncapacitated
	case ErrorOperationConflict:
		return ErrOperationConflict
	case ErrorExhausted:
		return ErrExhausted
	default:
		return nil
	}
}

// Error returns a fixed safe message, including for unrecognized kinds.
func (e *DomainError) Error() string {
	if category := e.category(); category != nil {
		return category.Error()
	}
	return "character operation failed"
}

// Is permits classification without comparing error strings.
func (e *DomainError) Is(target error) bool { return e.category() != nil && target == e.category() }

// StorageError preserves an unexpected database cause for runtime classification
// while keeping its SQL, arguments, and driver text out of the error message.
type StorageError struct{ cause error }

// NewStorageError wraps a storage cause without exposing its text. Callers must
// propagate this error to the owner of the transaction for rollback and retry.
func NewStorageError(cause error) *StorageError { return &StorageError{cause: cause} }

func (e *StorageError) Error() string        { return "character storage operation failed" }
func (e *StorageError) Unwrap() error        { return e.cause }
func (e *StorageError) Is(target error) bool { return target == ErrStorage }

// StorageFailure identifies failures that must reach the transaction owner even
// when their underlying cause is not a driver-specific error.
func (*StorageError) StorageFailure() {}
