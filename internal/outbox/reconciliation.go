package outbox

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var (
	ErrInvalidResolution = errors.New("outbox: invalid reconciliation resolution or state")
	ErrRetryExhausted    = errors.New("outbox: reconciliation retry budget exhausted")
	ErrReconciliation    = errors.New("outbox: reconciliation handler failed")
)

// ReconciliationDecision is an explicit feature or operator decision, never an
// inference from an interrupted transport attempt.
type ReconciliationDecision string

const (
	ReconciliationConfirm   ReconciliationDecision = "confirm"
	ReconciliationRetry     ReconciliationDecision = "safe_retry"
	ReconciliationPermanent ReconciliationDecision = "permanent"
)

// ReconciliationResolution supplies a closed decision and a privacy-safe audit ID.
// Retry asserts that resending is safe; it does not reset the original budget.
type ReconciliationResolution struct {
	Decision      ReconciliationDecision
	CorrelationID runtimecontract.CorrelationID
}

// Delivery contains private feature payload and immutable attempt facts. It must
// never be included in operational diagnostics.
type Delivery struct {
	ID          runtimecontract.DeliveryID
	Kind        runtimecontract.DeliveryKind
	Payload     runtimecontract.VersionedPayload
	Attempts    int
	MaxAttempts int
	updatedAt   int64
}

// Reconciler must honor context cancellation and must not send the delivery.
// Only its returned explicit decision may release work for a later worker send.
type Reconciler interface {
	Reconcile(context.Context, Delivery) (ReconciliationResolution, error)
}

// RegisterReconciler installs one owner per kind/version before Seal/readiness.
func (s *Store) RegisterReconciler(kind runtimecontract.DeliveryKind, version uint16, reconciler Reconciler) error {
	if kind == "" || version == 0 || reconciler == nil {
		return ErrInvalidResolution
	}

	value := reflect.ValueOf(reconciler)
	switch value.Kind() {
	case reflect.Pointer, reflect.Func, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan:
		if value.IsNil() {
			return ErrInvalidResolution
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.sealed {
		return ErrRegistrySealed
	}

	key := schemaKey{kind, version}
	if _, ok := s.reconcilers[key]; ok {
		return ErrRegistrationConflict
	}

	s.reconcilers[key] = reconciler
	return nil
}

// Reconcile offers a quarantined delivery to its exact registered schema owner.
// False means no matching reconciler; the record remains operator-visible. The
// caller owns scheduling. No transaction or background goroutine spans the call.
func (s *Store) Reconcile(ctx context.Context, id runtimecontract.DeliveryID, bound time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	if bound <= 0 {
		return false, ErrInvalidResolution
	}

	s.mu.RLock()
	sealed := s.sealed
	s.mu.RUnlock()
	if !sealed {
		return false, ErrRegistrationsOpen
	}

	d, err := s.reconciliationDelivery(ctx, s.db, id)
	if err != nil {
		return false, err
	}

	s.mu.RLock()
	handler := s.reconcilers[schemaKey{d.Kind, d.Payload.Version()}]
	s.mu.RUnlock()
	if handler == nil {
		return false, nil
	}

	bounded, cancel := context.WithTimeout(ctx, bound)
	defer cancel()

	resolution, err := invokeReconciler(bounded, handler, d)
	if bounded.Err() != nil {
		return true, bounded.Err()
	}

	if err != nil {
		return true, ErrReconciliation
	}

	return true, s.resolve(ctx, id, resolution, &d)
}

func invokeReconciler(ctx context.Context, handler Reconciler, d Delivery) (r ReconciliationResolution, err error) {
	defer func() {
		if recover() != nil {
			r = ReconciliationResolution{}
			err = ErrReconciliation
		}
	}()

	return handler.Reconcile(ctx, d)
}

// Resolve atomically applies an explicit decision only to quarantined work.
// Original attempt records and counts are preserved, including uncertain sends.
func (s *Store) Resolve(ctx context.Context, id runtimecontract.DeliveryID, resolution ReconciliationResolution) error {
	return s.resolve(ctx, id, resolution, nil)
}

func (s *Store) resolve(ctx context.Context, id runtimecontract.DeliveryID, r ReconciliationResolution, fence *Delivery) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	state, reason, outcome := StateCompleted, "reconciliation_confirmed", observability.OutcomeCompleted
	switch r.Decision {
	case ReconciliationConfirm:
	case ReconciliationRetry:
		state, reason, outcome = StatePending, "reconciliation_retry", observability.OutcomeRetryable
	case ReconciliationPermanent:
		state, reason, outcome = StatePermanentFailure, "reconciliation_permanent", observability.OutcomePermanentFailure
	default:
		return ErrInvalidResolution
	}

	event, err := observability.NewDiagnostic(observability.CategoryReconciliation, r.CorrelationID, observability.OperationReconcile, outcome, observability.SafeFields{State: string(state)})
	if err != nil {
		return ErrInvalidResolution
	}

	transactor, ok := s.db.(runtimecontract.Transactor)
	if !ok || isNilDBTX(s.db) {
		return ErrTransactionRequired
	}

	err = transactor.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		d, err := s.reconciliationDelivery(ctx, tx, id)
		if err != nil {
			return err
		}

		if fence != nil && (d.Attempts != fence.Attempts || d.updatedAt != fence.updatedAt) {
			return ErrInvalidResolution
		}

		if r.Decision == ReconciliationRetry && d.Attempts >= d.MaxAttempts {
			return ErrRetryExhausted
		}

		now := s.now().UTC().UnixMilli()
		var next, terminal any

		if state == StatePending {
			next = now
		} else {
			terminal = now
		}
		result, err := tx.ExecContext(ctx, `UPDATE outbound_deliveries SET state=?,reason_code=?,next_attempt_at=?,terminal_at=?,updated_at=? WHERE id=? AND state='reconciliation_required' AND attempt_count=? AND updated_at=?`, state, reason, next, terminal, now, id, d.Attempts, d.updatedAt)
		if err != nil {
			return err
		}

		count, err := result.RowsAffected()
		if err != nil {
			return err
		}

		if count != 1 {
			return ErrInvalidResolution
		}

		return nil
	})
	if err != nil {
		for _, known := range []error{ErrNotFound, ErrInvalidResolution, ErrRetryExhausted} {
			if errors.Is(err, known) {
				return known
			}
		}
		return storageError(ctx, err)
	}

	s.diagnostics.Emit(ctx, event)
	return nil
}

func (s *Store) reconciliationDelivery(ctx context.Context, tx runtimecontract.DBTX, id runtimecontract.DeliveryID) (Delivery, error) {
	if id == "" {
		return Delivery{}, ErrInvalidResolution
	}

	var d Delivery
	var state State
	var version int
	var content []byte

	err := tx.QueryRowContext(ctx, `SELECT id,kind,content_version,content,attempt_count,max_attempts,updated_at,state FROM outbound_deliveries WHERE id=?`, id).Scan(&d.ID, &d.Kind, &version, &content, &d.Attempts, &d.MaxAttempts, &d.updatedAt, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return Delivery{}, ErrNotFound
	}

	if err != nil {
		return Delivery{}, storageError(ctx, err)
	}

	if state != StateReconciliationRequired {
		return Delivery{}, ErrInvalidResolution
	}

	if version < 1 || version > 65535 {
		return Delivery{}, ErrUnsupportedVersion
	}

	d.Payload, err = runtimecontract.NewVersionedPayload(uint16(version), content)
	if err != nil {
		return Delivery{}, ErrDecode
	}

	return d, nil
}
