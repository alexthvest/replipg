package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Job outcome kinds determine whether feature writes commit or roll back.
const (
	JobCompleted        JobOutcomeKind = "completed"
	JobRetryable        JobOutcomeKind = "retryable"
	JobPermanentFailure JobOutcomeKind = "permanent_failure"
	JobFailed           JobOutcomeKind = "failed"
	JobCancelled        JobOutcomeKind = "cancelled"
)

var (
	ErrClaimLost       = errors.New("scheduler: claim lost")
	ErrInvalidWorker   = errors.New("scheduler: invalid worker configuration")
	ErrJobNotCompleted = errors.New("scheduler: handler did not complete")
)

// WorkerOptions supplies the lease identity and a transaction-bound scope.
// Factories must be safe for concurrent calls. NewWorkScope must bind every
// operation to its supplied transaction and must not perform network calls.
type WorkerOptions struct {
	Owner            string
	Lease            time.Duration
	NewCorrelationID func() (runtimecontract.CorrelationID, error)
	NewWorkScope     func(runtimecontract.DBTX) runtimecontract.WorkScope
}

// Worker runs one handler at a time and owns no background goroutines.
type Worker struct {
	service *Service
	options WorkerOptions
	mu      sync.Mutex
}

// Claim is an opaque lease returned only after its claim transaction commits.
type Claim struct {
	job         Job
	owner       string
	correlation runtimecontract.CorrelationID
}

// NewWorker validates transaction and scope dependencies before work is claimed.
func NewWorker(s *Service, options WorkerOptions) (*Worker, error) {
	if s == nil || isNil(s.clock) || options.Owner == "" || options.Lease <= 0 || options.NewWorkScope == nil {
		return nil, ErrInvalidWorker
	}
	if tx, ok := s.db.(runtimecontract.Transactor); !ok || isNil(tx) {
		return nil, ErrTransactionRequired
	}
	if options.NewCorrelationID == nil {
		options.NewCorrelationID = observability.GenerateCorrelationID
	}
	return &Worker{service: s, options: options}, nil
}

// ClaimDue conditionally claims the earliest eligible job, or returns nil.
// A claim consumes one attempt. Expired claims are left for startup recovery.
func (w *Worker) ClaimDue(ctx context.Context) (*Claim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.service.mu.RLock()
	sealed := w.service.sealed
	w.service.mu.RUnlock()
	if !sealed {
		return nil, ErrRegistrationsOpen
	}
	correlation, err := w.options.NewCorrelationID()
	if err != nil || correlation == "" {
		return nil, ErrInvalidWorker
	}
	var claim *Claim
	err = w.service.db.(runtimecontract.Transactor).WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		now := w.service.clock.Now().UTC()
		var req runtimecontract.JobRequest
		var version int
		var payload []byte
		var due, initial, maxDelay int64
		err := tx.QueryRowContext(ctx, `UPDATE scheduled_jobs SET state='claimed',attempt_count=attempt_count+1,next_attempt_at=NULL,claim_owner=?,claim_correlation_id=?,lease_until=?,updated_at=? WHERE id=(SELECT id FROM scheduled_jobs WHERE due_at<=? AND attempt_count<max_attempts AND (state='pending' OR (state='retrying' AND next_attempt_at<=?)) ORDER BY CASE WHEN state='retrying' THEN next_attempt_at ELSE due_at END,id LIMIT 1) AND due_at<=? AND attempt_count<max_attempts AND (state='pending' OR (state='retrying' AND next_attempt_at<=?)) RETURNING id,job_type,payload_version,payload,due_at,max_attempts,initial_delay_ms,max_delay_ms`, w.options.Owner, correlation, ceilInstant(now.Add(w.options.Lease)), now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), now.UnixMilli()).Scan(&req.ID, &req.Type, &version, &payload, &due, &req.Retry.MaxAttempts, &initial, &maxDelay)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		w.service.mu.RLock()
		_, supported := w.service.handlers[registrationKey{req.Type, uint16(version)}]
		w.service.mu.RUnlock()
		if version < 1 || version > 65535 || !supported {
			return ErrUnsupportedVersion
		}
		req.Payload, err = runtimecontract.NewVersionedPayload(uint16(version), payload)
		if err != nil {
			return err
		}
		req.DueAt = time.UnixMilli(due).UTC()
		req.Retry.InitialDelay = durationFromMillis(initial)
		req.Retry.MaxDelay = durationFromMillis(maxDelay)
		claim = &Claim{job: Job{Request: req}, owner: w.options.Owner, correlation: correlation}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrUnsupportedVersion) {
			return nil, ErrUnsupportedVersion
		}
		return nil, storeError("claim", err)
	}
	return claim, nil
}

// ExecuteClaim fences a current lease before invoking the handler. A successful
// return means feature state, scope effects and completion all committed.
// Non-completed outcomes roll back before a separate fenced transition commits.
// ErrJobNotCompleted means that transition committed without completing the job.
func (w *Worker) ExecuteClaim(ctx context.Context, claim *Claim) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.executeClaim(ctx, claim)
}

func (w *Worker) executeClaim(ctx context.Context, claim *Claim) error {
	if claim == nil || claim.owner != w.options.Owner {
		return ErrClaimLost
	}
	var outcome JobOutcome
	err := w.service.db.(runtimecontract.Transactor).WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := w.fence(ctx, tx, claim, false); err != nil {
			return err
		}
		w.service.mu.RLock()
		handler := w.service.handlers[registrationKey{claim.job.Request.Type, claim.job.Request.Payload.Version()}]
		w.service.mu.RUnlock()
		if isNil(handler) {
			return ErrUnsupportedVersion
		}
		scope := w.options.NewWorkScope(tx)
		if isNil(scope) {
			return ErrInvalidWorker
		}
		outcome = executeHandler(ctx, handler, scope, claim.job)
		if outcome.Kind != JobCompleted {
			return ErrJobNotCompleted
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return w.fence(ctx, tx, claim, true)
	})
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, ErrJobNotCompleted) {
		if transitionErr := w.recordOutcome(ctx, claim, outcome); transitionErr != nil {
			if errors.Is(transitionErr, ErrClaimLost) {
				return ErrClaimLost
			}
			return storeError("outcome", transitionErr)
		}
		return ErrJobNotCompleted
	}
	for _, known := range []error{ErrClaimLost, ErrJobNotCompleted, ErrUnsupportedVersion, ErrInvalidWorker} {
		if errors.Is(err, known) {
			return known
		}
	}
	return storeError("execute", err)
}

// Recover only at the handler boundary. Internal scheduler failures are not
// mistaken for a feature failure, and panic values are never retained.
func executeHandler(ctx context.Context, handler JobHandler, scope runtimecontract.WorkScope, job Job) (outcome JobOutcome) {
	defer func() {
		if recover() != nil {
			outcome = JobOutcome{Kind: JobPermanentFailure, ReasonCode: "handler_panic"}
		}
	}()
	return handler.Execute(ctx, scope, job)
}

func (w *Worker) recordOutcome(ctx context.Context, claim *Claim, outcome JobOutcome) error {
	return w.service.db.(runtimecontract.Transactor).WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := w.fence(ctx, tx, claim, false); err != nil {
			return err
		}
		now := w.service.clock.Now().UTC()
		state, reason := StatePermanentFailure, safeReason(outcome.ReasonCode)
		var next, terminal any
		terminal = now.UnixMilli()
		switch outcome.Kind {
		case JobRetryable:
			var attempts, maxAttempts int
			var initial, maximum int64
			if err := tx.QueryRowContext(ctx, `SELECT attempt_count,max_attempts,initial_delay_ms,max_delay_ms FROM scheduled_jobs WHERE id=?`, claim.job.Request.ID).Scan(&attempts, &maxAttempts, &initial, &maximum); err != nil {
				return err
			}
			if attempts >= maxAttempts {
				reason = "retry_exhausted"
			} else {
				delay := retryDelayMillis(initial, maximum, attempts)
				nextAt := ceilInstant(now) + delay
				if floor := ceilInstant(now.Add(outcome.RetryAfter)); floor > nextAt {
					nextAt = floor
				}
				state, next, terminal = StateRetrying, nextAt, nil
			}
		case JobPermanentFailure:
		case JobCancelled:
			state, reason = StateCancelled, "cancelled"
		default:
			// JobHandler has no error return. Failed, zero and unknown outcomes
			// represent handler errors and are never automatically retried.
			reason = "handler_error"
		}
		result, err := tx.ExecContext(ctx, `UPDATE scheduled_jobs SET state=?,next_attempt_at=?,reason_code=?,claim_owner=NULL,claim_correlation_id=NULL,lease_until=NULL,updated_at=?,terminal_at=? WHERE id=? AND state='claimed' AND claim_owner=? AND claim_correlation_id=? AND lease_until>?`, state, next, reason, now.UnixMilli(), terminal, claim.job.Request.ID, claim.owner, claim.correlation, now.UnixMilli())
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrClaimLost
		}
		return nil
	})
}

func retryDelayMillis(initial, maximum int64, attempts int) int64 {
	delay := initial
	for attempt := 1; attempt < attempts && delay < maximum; attempt++ {
		if delay > maximum-delay {
			return maximum
		}
		delay *= 2
	}
	return min(delay, maximum)
}

func durationFromMillis(ms int64) time.Duration {
	if ms > math.MaxInt64/int64(time.Millisecond) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ms) * time.Millisecond
}

func (w *Worker) fence(ctx context.Context, tx runtimecontract.DBTX, claim *Claim, complete bool) error {
	now := w.service.clock.Now().UTC().UnixMilli()
	query := `UPDATE scheduled_jobs SET updated_at=updated_at WHERE id=? AND state='claimed' AND claim_owner=? AND claim_correlation_id=? AND lease_until>?`
	args := []any{claim.job.Request.ID, claim.owner, claim.correlation, now}
	if complete {
		query = `UPDATE scheduled_jobs SET state='completed',claim_owner=NULL,claim_correlation_id=NULL,lease_until=NULL,reason_code=NULL,updated_at=?,terminal_at=? WHERE id=? AND state='claimed' AND claim_owner=? AND claim_correlation_id=? AND lease_until>?`
		args = append([]any{now, now}, args...)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrClaimLost
	}
	return nil
}

// RunOnce claims and executes at most one due job. It reports true only after
// completion commits. The caller owns polling, cancellation and shutdown.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	claim, err := w.ClaimDue(ctx)
	if err != nil || claim == nil {
		return false, err
	}
	if err := w.executeClaim(ctx, claim); err != nil {
		return false, err
	}
	return true, nil
}
