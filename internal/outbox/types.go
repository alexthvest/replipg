// Package outbox persists recoverable outbound delivery intent.
package outbox

import (
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var (
	ErrConflict             = errors.New("delivery ID conflict")
	ErrNotFound             = errors.New("delivery not found")
	ErrUnsupportedVersion   = errors.New("unsupported delivery kind or version")
	ErrRegistrationConflict = errors.New("delivery schema already registered")
	ErrRegistrySealed       = errors.New("delivery schema registry sealed")
	ErrDecode               = errors.New("delivery payload decoding failed")
	ErrStorage              = errors.New("delivery storage operation failed")
)

// State is an authoritative durable delivery state.
type State string

const (
	StatePending                State = "pending"
	StateAttempting             State = "attempting"
	StateRetrying               State = "retrying"
	StateReconciliationRequired State = "reconciliation_required"
	StateCompleted              State = "completed"
	StatePermanentFailure       State = "permanent_failure"
)

// DeliveryStatus deliberately excludes request content, destination and authorization.
type DeliveryStatus struct {
	ID            runtimecontract.DeliveryID
	State         State
	Attempts      int
	MaxAttempts   int
	NextAttemptAt time.Time
	ReasonCode    string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	TerminalAt    time.Time
}

// ConflictError identifies a generated ID collision without including caller data.
type ConflictError struct{}

func (*ConflictError) Error() string        { return ErrConflict.Error() }
func (*ConflictError) Is(target error) bool { return target == ErrConflict }

// UnsupportedVersionError reports an unregistered delivery schema without its kind.
type UnsupportedVersionError struct{ Version uint16 }

func (*UnsupportedVersionError) Error() string        { return ErrUnsupportedVersion.Error() }
func (*UnsupportedVersionError) Is(target error) bool { return target == ErrUnsupportedVersion }

// Decoder is a feature-owned pure serialization mapping, not a gameplay handler.
// It maps opaque payload bytes to a transport-neutral public or ephemeral response.
// The original bytes are preserved unchanged in storage.
type Decoder func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error)

// Options supplies deterministic time and ID generation when needed. Nil functions
// use the system clock and crypto/rand respectively. Functions must be concurrency-safe.
type Options struct {
	// Diagnostics receives content-free reconciliation decisions. Nil uses stderr JSON.
	Diagnostics observability.DiagnosticSink
	Now         func() time.Time
	GenerateID  func() (runtimecontract.DeliveryID, error)
}
