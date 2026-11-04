package runtimecontract

// ValidationReason is a stable, content-free reason for rejecting a value.
type ValidationReason string

const (
	ReasonRequired     ValidationReason = "required"
	ReasonInvalid      ValidationReason = "invalid"
	ReasonUnsupported  ValidationReason = "unsupported"
	ReasonOutOfRange   ValidationReason = "out_of_range"
	ReasonInconsistent ValidationReason = "inconsistent"
)

// ValidationError identifies an invalid field without retaining or rendering
// its value, which may contain private response content or an opaque payload.
type ValidationError struct {
	Field  string
	Reason ValidationReason
}

func (e *ValidationError) Error() string { return e.Field + ": " + string(e.Reason) }

func validationError(field string, reason ValidationReason) error {
	return &ValidationError{Field: field, Reason: reason}
}
