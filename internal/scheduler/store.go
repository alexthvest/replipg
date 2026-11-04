// Package scheduler owns durable feature work and its handler registrations.
package scheduler

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var (
	ErrNotFound             = errors.New("scheduler: job not found")
	ErrConflict             = errors.New("scheduler: conflicting job identity")
	ErrUnsupportedVersion   = errors.New("scheduler: unsupported payload version")
	ErrUnregisteredType     = errors.New("scheduler: unregistered job type")
	ErrRegistrationsClosed  = errors.New("scheduler: registrations closed")
	ErrRegistrationsOpen    = errors.New("scheduler: registrations not sealed")
	ErrRegistrationConflict = errors.New("scheduler: registration already owned")
	ErrInvalidRegistration  = errors.New("scheduler: invalid registration")
	ErrTransactionRequired  = errors.New("scheduler: transaction required")
	ErrInvalidID            = errors.New("scheduler: job identity required")
	ErrCancellationConflict = errors.New("scheduler: cancellation conflict")
)

type Clock interface{ Now() time.Time }

type Job struct{ Request runtimecontract.JobRequest }

type JobOutcomeKind string

type JobOutcome struct {
	Kind       JobOutcomeKind
	RetryAfter time.Duration
	ReasonCode string
}

type JobHandler interface {
	Execute(context.Context, runtimecontract.WorkScope, Job) JobOutcome
}

type State string

const (
	StatePending          State = "pending"
	StateClaimed          State = "claimed"
	StateRetrying         State = "retrying"
	StateCompleted        State = "completed"
	StateCancelled        State = "cancelled"
	StatePermanentFailure State = "permanent_failure"
)

// CancellationConflictError reports the durable state preventing cancellation.
type CancellationConflictError struct{ State State }

func (e *CancellationConflictError) Error() string { return "scheduler: cancellation conflict" }

func (e *CancellationConflictError) Is(target error) bool { return target == ErrCancellationConflict }

// Cancel durably cancels pending or retrying work in its own short transaction.
// Already cancelled jobs return CancellationConflictError with StateCancelled;
// repeated calls never rewrite the original cancellation timestamp.
func (s *Service) Cancel(ctx context.Context, id runtimecontract.JobID) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if id == "" {
		return ErrInvalidID
	}

	transactor, ok := s.db.(runtimecontract.Transactor)
	if !ok || isNil(transactor) {
		return ErrTransactionRequired
	}

	err := transactor.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		now := s.clock.Now().UTC().UnixMilli()
		result, err := tx.ExecContext(ctx, `UPDATE scheduled_jobs SET state='cancelled',next_attempt_at=NULL,reason_code='cancelled',updated_at=?,terminal_at=? WHERE id=? AND state IN ('pending','retrying')`, now, now, id)
		if err != nil {
			return storeError("cancel", err)
		}

		changed, err := result.RowsAffected()
		if err != nil {
			return storeError("cancel", err)
		}

		if changed == 1 {
			return nil
		}

		var state State

		err = tx.QueryRowContext(ctx, `SELECT state FROM scheduled_jobs WHERE id=?`, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}

		if err != nil {
			return storeError("cancel", err)
		}

		return &CancellationConflictError{State: state}
	})
	if err == nil {
		return nil
	}

	var conflict *CancellationConflictError

	if errors.As(err, &conflict) {
		return conflict
	}

	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}

	return storeError("cancel", err)
}

type JobStatus struct {
	ID            runtimecontract.JobID
	State         State
	DueAt         time.Time
	AttemptCount  int
	NextAttemptAt *time.Time
	ReasonCode    string
}

type registrationKey struct {
	jobType runtimecontract.JobType
	version uint16
}

type Service struct {
	db       runtimecontract.DBTX
	clock    Clock
	mu       sync.RWMutex
	sealed   bool
	handlers map[registrationKey]JobHandler
}

// New accepts a DBTX for scheduling/status compatibility. Cancel additionally
// requires db to implement runtimecontract.Transactor; otherwise it returns
// ErrTransactionRequired before accessing storage.
func New(db runtimecontract.DBTX, clock Clock) *Service {
	return &Service{db: db, clock: clock, handlers: make(map[registrationKey]JobHandler)}
}

func (s *Service) Register(jobType runtimecontract.JobType, version uint16, handler JobHandler) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sealed {
		return ErrRegistrationsClosed
	}

	if jobType == "" || version == 0 || isNil(handler) {
		return ErrInvalidRegistration
	}

	key := registrationKey{jobType, version}
	if _, exists := s.handlers[key]; exists {
		return ErrRegistrationConflict
	}

	s.handlers[key] = handler
	return nil
}

func (s *Service) SealRegistrations() { s.mu.Lock(); defer s.mu.Unlock(); s.sealed = true }

func (s *Service) Schedule(ctx context.Context, tx runtimecontract.DBTX, req runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if err := req.Validate(); err != nil {
		return "", err
	}

	s.mu.RLock()
	sealed := s.sealed
	_, supported := s.handlers[registrationKey{req.Type, req.Payload.Version()}]
	knownType := false
	for key := range s.handlers {
		if key.jobType == req.Type {
			knownType = true
			break
		}
	}

	s.mu.RUnlock()
	if !sealed {
		return "", ErrRegistrationsOpen
	}

	if !knownType {
		return "", ErrUnregisteredType
	}

	if !supported {
		return "", ErrUnsupportedVersion
	}

	if isNil(tx) {
		return "", ErrTransactionRequired
	}

	due := ceilInstant(req.DueAt)
	initialDelay, maxDelay := ceilDuration(req.Retry.InitialDelay), ceilDuration(req.Retry.MaxDelay)
	now := s.clock.Now().UTC().UnixMilli()
	result, err := tx.ExecContext(ctx, `INSERT INTO scheduled_jobs
		(id,job_type,payload_version,payload,due_at,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,created_at,updated_at)
		VALUES (?,?,?,?,?,'pending',?,?,?,0,?,?) ON CONFLICT(id) DO NOTHING`,
		req.ID, req.Type, req.Payload.Version(), req.Payload.Data(), due, req.Retry.MaxAttempts, initialDelay, maxDelay, now, now)
	if err != nil {
		return "", storeError("schedule", err)
	}

	inserted, err := result.RowsAffected()
	if err != nil {
		return "", storeError("schedule", err)
	}

	if inserted == 0 {
		var jobType string
		var version int
		var payload []byte
		var storedDue, storedInitial, storedMax int64
		var attempts int

		err := tx.QueryRowContext(ctx, `SELECT job_type,payload_version,payload,due_at,max_attempts,initial_delay_ms,max_delay_ms FROM scheduled_jobs WHERE id=?`, req.ID).Scan(&jobType, &version, &payload, &storedDue, &attempts, &storedInitial, &storedMax)
		if err != nil {
			return "", storeError("compare", err)
		}

		if jobType != string(req.Type) || version != int(req.Payload.Version()) || !bytes.Equal(payload, req.Payload.Data()) || storedDue != due || attempts != req.Retry.MaxAttempts || storedInitial != initialDelay || storedMax != maxDelay {
			return "", ErrConflict
		}
	}
	return req.ID, nil
}

func (s *Service) Status(ctx context.Context, id runtimecontract.JobID) (JobStatus, error) {
	if id == "" {
		return JobStatus{}, ErrInvalidID
	}

	var status JobStatus
	var due int64
	var next sql.NullInt64
	var reason sql.NullString

	err := s.db.QueryRowContext(ctx, `SELECT id,state,due_at,attempt_count,next_attempt_at,reason_code FROM scheduled_jobs WHERE id=?`, id).Scan(&status.ID, &status.State, &due, &status.AttemptCount, &next, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return JobStatus{}, ErrNotFound
	}

	if err != nil {
		return JobStatus{}, storeError("status", err)
	}

	status.DueAt = time.UnixMilli(due).UTC()
	if next.Valid {
		value := time.UnixMilli(next.Int64).UTC()
		status.NextAttemptAt = &value
	}
	if reason.Valid {
		status.ReasonCode = safeReason(reason.String)
	}
	return status, nil
}

// StoreError exposes the operation without including database values or payloads.
type StoreError struct {
	Operation string
	cause     error
}

func (e *StoreError) Error() string { return "scheduler: " + e.Operation + " failed" }

func (e *StoreError) Unwrap() error { return e.cause }

func storeError(operation string, err error) error {
	var safe error

	for _, candidate := range []error{context.Canceled, context.DeadlineExceeded, persistence.ErrContention, persistence.ErrPersistenceClosed} {
		if errors.Is(err, candidate) {
			safe = candidate
			break
		}
	}
	return &StoreError{Operation: operation, cause: safe}
}

func isNil(value any) bool {
	if value == nil {
		return true
	}

	switch reflect.ValueOf(value).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(value).IsNil()
	default:
		return false
	}
}

func safeReason(reason string) string {
	switch reason {
	case "handler_error", "handler_panic", "handler_timeout", "retry_exhausted", "permanent_failure", "cancelled":
		return reason
	default:
		return "unspecified_failure"
	}
}

func ceilDuration(d time.Duration) int64 {
	ms := int64(d / time.Millisecond)
	if d%time.Millisecond != 0 {
		ms++
	}
	return ms
}

func ceilInstant(t time.Time) int64 {
	ms := t.UnixMilli()
	if time.UnixMilli(ms).Before(t) {
		ms++
	}
	return ms
}
