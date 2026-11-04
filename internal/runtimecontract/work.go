package runtimecontract

import (
	"context"
	"time"
)

// UpdateID identifies a Telegram update without exposing a Telegram model.
type UpdateID int64

// ChatID identifies a Telegram chat without exposing a Telegram model.
type ChatID int64

// UserID identifies a Telegram user without exposing a Telegram model.
type UserID int64

// DeliveryID is an opaque runtime-owned outbound delivery identifier.
type DeliveryID string

// JobID is an opaque runtime-owned scheduled-job identifier.
type JobID string

// CorrelationID is an opaque runtime-owned operational correlation identifier.
type CorrelationID string

// DeliveryKind identifies a feature-owned recoverable delivery schema.
type DeliveryKind string

// JobType identifies a feature-owned scheduled job schema.
type JobType string

// RetryPolicy bounds attempts and exponential-delay growth for durable work.
type RetryPolicy struct {
	MaxAttempts  int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

// Validate reports whether the retry policy is bounded and internally
// consistent.
func (p RetryPolicy) Validate() error {
	if p.MaxAttempts < 1 {
		return validationError("retry_policy.max_attempts", ReasonOutOfRange)
	}
	if p.InitialDelay <= 0 {
		return validationError("retry_policy.initial_delay", ReasonOutOfRange)
	}
	if p.MaxDelay <= 0 {
		return validationError("retry_policy.max_delay", ReasonOutOfRange)
	}
	if p.MaxDelay < p.InitialDelay {
		return validationError("retry_policy.max_delay", ReasonInconsistent)
	}
	return nil
}

// VersionedPayload is an opaque, integer-versioned feature payload. Its byte
// storage is copied on construction and access so callers cannot mutate a
// request after validation.
type VersionedPayload struct {
	version uint16
	data    []byte
}

// NewVersionedPayload validates and copies an opaque payload.
func NewVersionedPayload(version uint16, data []byte) (VersionedPayload, error) {
	if version == 0 {
		return VersionedPayload{}, validationError("payload.version", ReasonOutOfRange)
	}
	if len(data) == 0 {
		return VersionedPayload{}, validationError("payload.data", ReasonRequired)
	}
	return VersionedPayload{version: version, data: append([]byte(nil), data...)}, nil
}

// Version returns the schema version carried by the payload.
func (p VersionedPayload) Version() uint16 { return p.version }

// Data returns a copy of the opaque payload bytes.
func (p VersionedPayload) Data() []byte { return append([]byte(nil), p.data...) }

// Validate reports structural payload errors.
func (p VersionedPayload) Validate() error {
	if p.version == 0 {
		return validationError("payload.version", ReasonOutOfRange)
	}
	if len(p.data) == 0 {
		return validationError("payload.data", ReasonRequired)
	}
	return nil
}

// ValidateVersion reports whether the payload uses one of the versions the
// consuming registry supports. The registry itself remains service-owned.
func (p VersionedPayload) ValidateVersion(supported ...uint16) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for _, version := range supported {
		if version != 0 && p.version == version {
			return nil
		}
	}
	return validationError("payload.version", ReasonUnsupported)
}

// DeliveryRequest is the shared recoverable delivery intent accepted by
// WorkScope. The payload schema is owned by the feature identified by Kind.
type DeliveryRequest struct {
	Kind    DeliveryKind
	Payload VersionedPayload
	Retry   RetryPolicy
}

// Validate reports structural errors before a delivery is persisted.
func (r DeliveryRequest) Validate() error {
	if r.Kind == "" {
		return validationError("delivery.kind", ReasonRequired)
	}
	if err := r.Payload.Validate(); err != nil {
		return err
	}
	return r.Retry.Validate()
}

// JobRequest is the shared scheduled-work intent accepted by WorkScope.
type JobRequest struct {
	ID      JobID
	Type    JobType
	DueAt   time.Time
	Payload VersionedPayload
	Retry   RetryPolicy
}

// Validate reports structural errors before a job is persisted.
func (r JobRequest) Validate() error {
	if r.ID == "" {
		return validationError("job.id", ReasonRequired)
	}
	if r.Type == "" {
		return validationError("job.type", ReasonRequired)
	}
	if r.DueAt.IsZero() {
		return validationError("job.due_at", ReasonRequired)
	}
	if err := r.Payload.Validate(); err != nil {
		return err
	}
	return r.Retry.Validate()
}

// WorkScope exposes the current transaction and the durable effects that can
// be recorded atomically with feature state. It deliberately provides no
// network or concrete service capability.
type WorkScope interface {
	DBTX() DBTX
	EnqueueDelivery(ctx context.Context, req DeliveryRequest) (DeliveryID, error)
	Schedule(ctx context.Context, req JobRequest) (JobID, error)
}
