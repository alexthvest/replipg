package contract

import "errors"

var (
	ErrNotOwned          = errors.New("inventory item not owned")
	ErrIncompatibleSlot  = errors.New("inventory incompatible equipment slot")
	ErrStaleControl      = errors.New("inventory control stale")
	ErrExpiredControl    = errors.New("inventory control expired")
	ErrNotUsable         = errors.New("inventory item not usable")
	ErrFullHealth        = errors.New("inventory healing rejected at full health")
	ErrInvalidInput      = errors.New("invalid inventory input")
	ErrInvalidCatalog    = errors.New("invalid inventory catalog")
	ErrNotFound          = errors.New("inventory not found")
	ErrIncapacitated     = errors.New("inventory incapacitated")
	ErrOperationConflict = errors.New("inventory operation conflict")
	ErrExhausted         = errors.New("inventory numeric range exhausted")
	ErrStorage           = errors.New("inventory storage operation failed")
)

// ErrorKind is an inspectable domain failure category.
type ErrorKind string

const (
	ErrorNotOwned          ErrorKind = "not_owned"
	ErrorIncompatibleSlot  ErrorKind = "incompatible_slot"
	ErrorStaleControl      ErrorKind = "stale_control"
	ErrorExpiredControl    ErrorKind = "expired_control"
	ErrorNotUsable         ErrorKind = "not_usable"
	ErrorFullHealth        ErrorKind = "full_health"
	ErrorInvalidInput      ErrorKind = "invalid_input"
	ErrorInvalidCatalog    ErrorKind = "invalid_catalog"
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
	case ErrorNotOwned:
		return ErrNotOwned
	case ErrorIncompatibleSlot:
		return ErrIncompatibleSlot
	case ErrorStaleControl:
		return ErrStaleControl
	case ErrorExpiredControl:
		return ErrExpiredControl
	case ErrorNotUsable:
		return ErrNotUsable
	case ErrorFullHealth:
		return ErrFullHealth
	case ErrorInvalidInput:
		return ErrInvalidInput
	case ErrorInvalidCatalog:
		return ErrInvalidCatalog
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

	return "inventory operation failed"
}

// Is permits classification without comparing error strings.
func (e *DomainError) Is(target error) bool { return e.category() != nil && target == e.category() }

// StorageError preserves an unexpected database cause for runtime classification
// while keeping its SQL, arguments, and driver text out of the error message.
type StorageError struct{ cause error }

// NewStorageError wraps a storage cause without exposing its text. Callers must
// propagate this error to the owner of the transaction for rollback and retry.
func NewStorageError(cause error) *StorageError { return &StorageError{cause: cause} }

func (e *StorageError) Error() string        { return "inventory storage operation failed" }
func (e *StorageError) Unwrap() error        { return e.cause }
func (e *StorageError) Is(target error) bool { return target == ErrStorage }

// StorageFailure identifies failures that must reach the transaction owner even
// when their underlying cause is not a driver-specific error.
func (*StorageError) StorageFailure() {}
