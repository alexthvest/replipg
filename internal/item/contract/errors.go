package contract

import "errors"

var (
	ErrInvalidInput   = errors.New("invalid item input")
	ErrInvalidCatalog = errors.New("invalid item catalog")
	ErrNotUsable      = errors.New("item not usable")
)

type ErrorKind string

const (
	ErrorInvalidInput   ErrorKind = "invalid_input"
	ErrorInvalidCatalog ErrorKind = "invalid_catalog"
	ErrorNotUsable      ErrorKind = "not_usable"
)

// DomainError never includes caller-supplied values in its message.
type DomainError struct {
	Kind  ErrorKind
	Field string
}

func (e *DomainError) category() error {
	switch e.Kind {
	case ErrorInvalidInput:
		return ErrInvalidInput
	case ErrorInvalidCatalog:
		return ErrInvalidCatalog
	case ErrorNotUsable:
		return ErrNotUsable
	}
	return nil
}

func (e *DomainError) Error() string {
	if c := e.category(); c != nil {
		return c.Error()
	}

	return "item operation failed"
}

func (e *DomainError) Is(target error) bool { return e.category() != nil && target == e.category() }
