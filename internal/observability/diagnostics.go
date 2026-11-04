// Package observability emits privacy-safe structured runtime diagnostics.
package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// EventCategory is one of the runtime's operational diagnostic groups.
type EventCategory string

const (
	CategoryStartup        EventCategory = "startup"
	CategoryIntake         EventCategory = "intake"
	CategoryDispatch       EventCategory = "dispatch"
	CategoryCallbackAck    EventCategory = "callback_ack"
	CategoryDelivery       EventCategory = "delivery"
	CategoryReconciliation EventCategory = "reconciliation"
	CategoryScheduler      EventCategory = "scheduler"
	CategoryStorage        EventCategory = "storage"
	CategoryHealth         EventCategory = "health"
	CategoryShutdown       EventCategory = "shutdown"
)

// Operations are the complete diagnostic operation vocabulary. New runtime
// behavior must deliberately extend this list rather than logging free-form
// values.
const (
	OperationValidate      = "validate"
	OperationPrepare       = "prepare"
	OperationMigrate       = "migrate"
	OperationRecover       = "recover"
	OperationPoll          = "poll"
	OperationReceive       = "receive"
	OperationDispatch      = "dispatch"
	OperationAcknowledge   = "acknowledge"
	OperationSend          = "send"
	OperationSendPublic    = "send_public"
	OperationSendEphemeral = "send_ephemeral"
	OperationAttempt       = "attempt"
	OperationReconcile     = "reconcile"
	OperationSchedule      = "schedule"
	OperationExecute       = "execute"
	OperationTransaction   = "transaction"
	OperationProbe         = "probe"
	OperationStateChange   = "state_change"
	OperationDrain         = "drain"
	OperationClose         = "close"
)

// Outcomes are the complete content-free result vocabulary.
const (
	OutcomeOK               = "ok"
	OutcomeAccepted         = "accepted"
	OutcomeCompleted        = "completed"
	OutcomeIgnored          = "ignored"
	OutcomeSkipped          = "skipped"
	OutcomeFailed           = "failed"
	OutcomeRetryable        = "retryable"
	OutcomeIndeterminate    = "indeterminate"
	OutcomePermanentFailure = "permanent_failure"
	OutcomeCancelled        = "cancelled"
)

// States are the complete lifecycle and durable-work state vocabulary.
const (
	StateStarting               = "starting"
	StateRecovering             = "recovering"
	StateReady                  = "ready"
	StateDraining               = "draining"
	StateStopped                = "stopped"
	StatePending                = "pending"
	StateAttempting             = "attempting"
	StateClaimed                = "claimed"
	StateRetrying               = "retrying"
	StateReconciliationRequired = "reconciliation_required"
	StateCompleted              = "completed"
	StateCancelled              = "cancelled"
	StatePermanentFailure       = "permanent_failure"
)

// SafeFields is the closed set of optional detail that a diagnostic may emit.
// Zero identifiers are treated as absent and are never emitted.
type SafeFields struct {
	Duration time.Duration
	RetryAt  time.Time
	State    string
	UpdateID runtimecontract.UpdateID
	ChatID   runtimecontract.ChatID
	UserID   runtimecontract.UserID
}

// Diagnostic is a validated, content-free operational event. Directly
// constructed values are revalidated by the sink before emission.
type Diagnostic struct {
	Category      EventCategory
	CorrelationID runtimecontract.CorrelationID
	Operation     string
	Outcome       string
	Fields        SafeFields
}

// DiagnosticSink accepts privacy-safe operational events.
type DiagnosticSink interface {
	Emit(ctx context.Context, event Diagnostic)
}

// ValidationReason describes an invalid diagnostic without retaining its
// rejected value.
type ValidationReason string

const (
	ReasonRequired   ValidationReason = "required"
	ReasonInvalid    ValidationReason = "invalid"
	ReasonOutOfRange ValidationReason = "out_of_range"
)

// ValidationError identifies only the rejected field and reason. It never
// contains the rejected value, which may be sensitive.
type ValidationError struct {
	Field  string
	Reason ValidationReason
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + string(e.Reason)
}

// NewDiagnostic validates and normalizes a diagnostic before use. Operation,
// outcome, and state are restricted to the exact declared vocabularies so they
// cannot be used as free-form message or error fields.
func NewDiagnostic(category EventCategory, correlationID runtimecontract.CorrelationID, operation, outcome string, fields SafeFields) (Diagnostic, error) {
	event := Diagnostic{
		Category:      category,
		CorrelationID: correlationID,
		Operation:     operation,
		Outcome:       outcome,
		Fields:        fields,
	}
	if err := validateDiagnostic(event); err != nil {
		return Diagnostic{}, err
	}

	if !event.Fields.RetryAt.IsZero() {
		event.Fields.RetryAt = event.Fields.RetryAt.UTC()
	}
	return event, nil
}

// GenerateCorrelationID creates a 128-bit opaque identifier using the
// operating system cryptographic random source.
func GenerateCorrelationID() (runtimecontract.CorrelationID, error) {
	var random [16]byte

	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}

	encoded := make([]byte, hex.EncodedLen(len(random)))
	hex.Encode(encoded, random[:])
	return runtimecontract.CorrelationID(encoded), nil
}

// JSONSink emits newline-delimited JSON diagnostics. It owns the slog logger
// configuration so caller logger attributes and context values cannot add
// fields to records.
type JSONSink struct {
	logger *slog.Logger
}

// NewJSONSink constructs a sink which emits only the diagnostic allowlist.
// A nil writer safely discards records.
func NewJSONSink(writer io.Writer) *JSONSink {
	if writer == nil {
		writer = io.Discard
	}
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			switch attr.Key {
			case slog.TimeKey, slog.LevelKey, slog.MessageKey, slog.SourceKey:
				return slog.Attr{}
			default:
				return attr
			}
		},
	})
	return &JSONSink{logger: slog.New(handler)}
}

// Emit writes a valid diagnostic and silently drops invalid directly-built
// values. Rejection itself emits nothing because the rejected value may be
// sensitive.
func (s *JSONSink) Emit(ctx context.Context, event Diagnostic) {
	if s == nil || s.logger == nil || validateDiagnostic(event) != nil {
		return
	}

	attrs := []slog.Attr{
		slog.String("category", string(event.Category)),
		slog.String("correlation_id", string(event.CorrelationID)),
		slog.String("operation", event.Operation),
		slog.String("outcome", event.Outcome),
	}
	if event.Fields.Duration > 0 {
		attrs = append(attrs, slog.Float64("duration_ms", float64(event.Fields.Duration)/float64(time.Millisecond)))
	}
	if !event.Fields.RetryAt.IsZero() {
		attrs = append(attrs, slog.Time("retry_at", event.Fields.RetryAt.UTC()))
	}
	if event.Fields.State != "" {
		attrs = append(attrs, slog.String("state", event.Fields.State))
	}
	if event.Fields.UpdateID != 0 {
		attrs = append(attrs, slog.Int64("update_id", int64(event.Fields.UpdateID)))
	}
	if event.Fields.ChatID != 0 {
		attrs = append(attrs, slog.Int64("chat_id", int64(event.Fields.ChatID)))
	}
	if event.Fields.UserID != 0 {
		attrs = append(attrs, slog.Int64("user_id", int64(event.Fields.UserID)))
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "", attrs...)
}

func validateDiagnostic(event Diagnostic) error {
	if !validCategory(event.Category) {
		return validationError("category", ReasonInvalid)
	}

	if !validCorrelationID(event.CorrelationID) {
		return validationError("correlation_id", ReasonInvalid)
	}

	if event.Operation == "" {
		return validationError("operation", ReasonRequired)
	}

	if !validOperation(event.Operation) {
		return validationError("operation", ReasonInvalid)
	}

	if event.Outcome == "" {
		return validationError("outcome", ReasonRequired)
	}

	if !validOutcome(event.Outcome) {
		return validationError("outcome", ReasonInvalid)
	}

	if event.Fields.Duration < 0 {
		return validationError("duration", ReasonOutOfRange)
	}

	if !event.Fields.RetryAt.IsZero() {
		year := event.Fields.RetryAt.Year()
		if year < 1 || year > 9999 {
			return validationError("retry_at", ReasonOutOfRange)
		}
	}
	if event.Fields.State != "" && !validState(event.Fields.State) {
		return validationError("state", ReasonInvalid)
	}

	return validateIdentifiers(event.Category, event.Fields)
}

func validCategory(category EventCategory) bool {
	switch category {
	case CategoryStartup, CategoryIntake, CategoryDispatch, CategoryCallbackAck,
		CategoryDelivery, CategoryReconciliation, CategoryScheduler,
		CategoryStorage, CategoryHealth, CategoryShutdown:
		return true
	default:
		return false
	}
}

func validCorrelationID(id runtimecontract.CorrelationID) bool {
	if len(id) != 32 {
		return false
	}

	for _, char := range id {
		if ('0' <= char && char <= '9') || ('a' <= char && char <= 'f') {
			continue
		}

		return false
	}
	return true
}

func validOperation(operation string) bool {
	switch operation {
	case OperationValidate, OperationPrepare, OperationMigrate, OperationRecover,
		OperationPoll, OperationReceive, OperationDispatch, OperationAcknowledge,
		OperationSend, OperationSendPublic, OperationSendEphemeral, OperationAttempt,
		OperationReconcile, OperationSchedule, OperationExecute, OperationTransaction,
		OperationProbe, OperationStateChange, OperationDrain, OperationClose:
		return true
	default:
		return false
	}
}

func validOutcome(outcome string) bool {
	switch outcome {
	case OutcomeOK, OutcomeAccepted, OutcomeCompleted, OutcomeIgnored,
		OutcomeSkipped, OutcomeFailed, OutcomeRetryable, OutcomeIndeterminate,
		OutcomePermanentFailure, OutcomeCancelled:
		return true
	default:
		return false
	}
}

func validState(state string) bool {
	switch state {
	case StateStarting, StateRecovering, StateReady, StateDraining, StateStopped,
		StatePending, StateAttempting, StateClaimed, StateRetrying,
		StateReconciliationRequired, StateCompleted, StateCancelled,
		StatePermanentFailure:
		return true
	default:
		return false
	}
}

func validateIdentifiers(category EventCategory, fields SafeFields) error {
	count := 0
	if fields.UpdateID != 0 {
		count++
	}
	if fields.ChatID != 0 {
		count++
	}
	if fields.UserID != 0 {
		count++
	}
	if count > 1 {
		return validationError("identifiers", ReasonOutOfRange)
	}

	if fields.UpdateID != 0 {
		switch category {
		case CategoryIntake, CategoryDispatch, CategoryCallbackAck:
		default:
			return validationError("update_id", ReasonInvalid)
		}
	}
	if fields.ChatID != 0 && category != CategoryDelivery {
		return validationError("chat_id", ReasonInvalid)
	}

	if fields.UserID != 0 && category != CategoryDelivery {
		return validationError("user_id", ReasonInvalid)
	}

	return nil
}

func validationError(field string, reason ValidationReason) error {
	return &ValidationError{Field: field, Reason: reason}
}
