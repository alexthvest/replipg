package outbox

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var (
	ErrClaimLost            = errors.New("outbox: claim lost")
	ErrDeliveryNotCompleted = errors.New("outbox: delivery not completed")
	ErrInvalidWorker        = errors.New("outbox: invalid worker configuration")
	ErrTransactionRequired  = errors.New("outbox: transaction capability required")
	ErrRegistrationsOpen    = errors.New("outbox: registrations must be sealed")
)

// DeliveryPort supplies transport-neutral evidence for a single delivery. Runtime
// composition maps Sender receipts and adapter errors into these typed outcomes.
// Implementations must honor cancellation and must not panic or expose raw errors.
type DeliveryPort interface {
	Deliver(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome
}

// WorkerOptions supplies delivery and concurrency-safe correlation generation.
// Nil NewCorrelationID uses crypto/rand through observability.
type WorkerOptions struct {
	Delivery         DeliveryPort
	NewCorrelationID func() (runtimecontract.CorrelationID, error)
	// Sleep must honor cancellation; nil uses a context-aware timer.
	Sleep func(context.Context, time.Duration) error
}

// Worker sends one intent at a time without owning background goroutines. Caller
// code owns wakeups and startup recovery; attempting work is never reclaimed here.
type Worker struct {
	store   *Store
	tx      runtimecontract.Transactor
	options WorkerOptions
	mu      sync.Mutex
}

// Claim is an opaque attempt returned only after attempting state and its attempt
// record commit together. Its execution token is shared even if Claim is copied.
type Claim struct {
	worker    *Worker
	id        runtimecontract.DeliveryID
	attemptID runtimecontract.DeliveryID
	payload   runtimecontract.VersionedPayload
	decoder   Decoder
	consumed  *atomic.Bool
}

// NewWorker requires the persistence kernel's transaction capability. Delivery
// schema registrations must be sealed before ClaimDue can select any work.
func NewWorker(store *Store, options WorkerOptions) (*Worker, error) {
	if store == nil || options.Delivery == nil {
		return nil, ErrInvalidWorker
	}

	value := reflect.ValueOf(options.Delivery)
	if (value.Kind() == reflect.Pointer || value.Kind() == reflect.Func || value.Kind() == reflect.Interface) && value.IsNil() {
		return nil, ErrInvalidWorker
	}

	tx, ok := store.db.(runtimecontract.Transactor)
	if !ok || isNilDBTX(store.db) {
		return nil, ErrTransactionRequired
	}

	if options.NewCorrelationID == nil {
		options.NewCorrelationID = observability.GenerateCorrelationID
	}
	if options.Sleep == nil {
		options.Sleep = sleepContext
	}
	return &Worker{store: store, tx: tx, options: options}, nil
}

// ClaimDue atomically claims the earliest due pending/retrying intent and appends
// an unfinished attempt. A nil claim means no work is eligible. The attempt is
// the durable ownership token; abandonment requires explicit startup recovery.
func (w *Worker) ClaimDue(ctx context.Context) (*Claim, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	w.store.mu.RLock()
	sealed := w.store.sealed
	w.store.mu.RUnlock()
	if !sealed {
		return nil, ErrRegistrationsOpen
	}

	attemptID, err := generateID()
	if err != nil {
		return nil, ErrInvalidWorker
	}

	correlation, err := w.options.NewCorrelationID()
	if err != nil || correlation == "" {
		return nil, ErrInvalidWorker
	}

	var claim *Claim

	err = w.tx.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		now := w.store.now().UTC().UnixMilli()
		var id runtimecontract.DeliveryID
		var kind runtimecontract.DeliveryKind
		var version int
		var content []byte

		err := tx.QueryRowContext(ctx, `UPDATE outbound_deliveries SET state='attempting',attempt_count=attempt_count+1,next_attempt_at=NULL,reason_code=NULL,updated_at=?
   WHERE id=(SELECT id FROM outbound_deliveries WHERE state IN ('pending','retrying') AND next_attempt_at<=? AND attempt_count<max_attempts ORDER BY next_attempt_at,id LIMIT 1)
   AND state IN ('pending','retrying') AND next_attempt_at<=? AND attempt_count<max_attempts
   RETURNING id,kind,content_version,content`, now, now, now).Scan(&id, &kind, &version, &content)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}

		if err != nil {
			return err
		}

		if version < 1 || version > 65535 {
			return ErrUnsupportedVersion
		}

		w.store.mu.RLock()
		decoder := w.store.decoders[schemaKey{kind, uint16(version)}]
		w.store.mu.RUnlock()
		if decoder == nil {
			return ErrUnsupportedVersion
		}

		payload, err := runtimecontract.NewVersionedPayload(uint16(version), content)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `INSERT INTO outbound_attempts(id,delivery_id,correlation_id,started_at) VALUES(?,?,?,?)`, attemptID, id, correlation, now)
		if err != nil {
			return err
		}

		claim = &Claim{worker: w, id: id, attemptID: attemptID, payload: payload, decoder: decoder, consumed: &atomic.Bool{}}
		return nil
	})
	if err != nil {
		return nil, workerStorageError(ctx, err)
	}

	return claim, nil
}

// ExecuteClaim delivers at most once for this ownership token. Outcomes update
// intent and attempt atomically; interrupted persistence leaves attempting work
// for startup reconciliation. ErrDeliveryNotCompleted means a nonaccepted
// outcome was durably recorded.
func (w *Worker) ExecuteClaim(ctx context.Context, claim *Claim) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.executeClaim(ctx, claim)
}

func (w *Worker) executeClaim(ctx context.Context, claim *Claim) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if claim == nil || claim.worker != w || claim.consumed == nil || !claim.consumed.CompareAndSwap(false, true) {
		return ErrClaimLost
	}

	var exists int

	err := w.store.db.QueryRowContext(ctx, `SELECT 1 FROM outbound_deliveries d JOIN outbound_attempts a ON a.delivery_id=d.id
  WHERE d.id=? AND d.state='attempting' AND a.id=? AND a.finished_at IS NULL`, claim.id, claim.attemptID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrClaimLost
	}

	if err != nil {
		return workerStorageError(ctx, err)
	}

	response, err := decode(claim.decoder, claim.payload)
	if err != nil {
		return w.recordFailure(ctx, claim, runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryPermanentFailure, ReasonCode: "invalid_request"})
	}

	if err := runtimecontract.ValidateResponseAt(response, w.store.now().UTC()); err != nil {
		reason := "invalid_request"
		var invalid *runtimecontract.ValidationError

		if errors.As(err, &invalid) && invalid.Field == "authorization.valid_until" {
			reason = "authorization_expired"
		}
		return w.recordFailure(ctx, claim, runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryPermanentFailure, ReasonCode: reason})
	}

	outcome := w.options.Delivery.Deliver(ctx, response)
	if outcome.Kind != runtimecontract.DeliveryAccepted || outcome.Receipt == nil {
		if err := ctx.Err(); err != nil {
			return err
		}

		return w.recordFailure(ctx, claim, outcome)
	}

	err = w.tx.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		now := w.store.now().UTC().UnixMilli()
		result, err := tx.ExecContext(ctx, `UPDATE outbound_deliveries SET state='completed',updated_at=?,terminal_at=?,reason_code=NULL
   WHERE id=? AND state='attempting' AND EXISTS(SELECT 1 FROM outbound_attempts WHERE id=? AND delivery_id=? AND finished_at IS NULL)`, now, now, claim.id, claim.attemptID, claim.id)
		if err != nil {
			return err
		}

		count, err := result.RowsAffected()
		if err != nil {
			return err
		}

		if count != 1 {
			return ErrClaimLost
		}

		result, err = tx.ExecContext(ctx, `UPDATE outbound_attempts SET finished_at=MAX(?,started_at),outcome='accepted',reason_code=NULL WHERE id=? AND delivery_id=? AND finished_at IS NULL`, now, claim.attemptID, claim.id)
		if err != nil {
			return err
		}

		count, err = result.RowsAffected()
		if err != nil {
			return err
		}

		if count != 1 {
			return ErrClaimLost
		}

		return nil
	})
	return workerStorageError(ctx, err)
}

// RunOnce claims and executes at most one eligible delivery. True means accepted
// completion committed. The caller owns polling, cancellation and shutdown.
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

func workerStorageError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}

	for _, known := range []error{ErrUnsupportedVersion, ErrClaimLost} {
		if errors.Is(err, known) {
			return known
		}
	}
	return storageError(ctx, err)
}
