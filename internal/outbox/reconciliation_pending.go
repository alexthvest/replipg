package outbox

import (
	"context"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// ReconcilePending performs one startup pass over a snapshot of quarantined IDs.
// Registrations must be sealed. Rows close before invoking any feature handler;
// work inserted during the pass is deferred to the next caller-owned pass.
// Missing handlers, invalid decisions, retry exhaustion and handler failures or
// timeouts remain visible and are diagnosed without failing startup. Parent
// cancellation, invalid setup and storage failures are returned to the caller.
// Reconciler cancellation remains cooperative. This method never sends messages.
func (s *Store) ReconcilePending(ctx context.Context, bound time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if bound <= 0 {
		return ErrInvalidResolution
	}

	s.mu.RLock()
	sealed := s.sealed
	s.mu.RUnlock()

	if !sealed {
		return ErrRegistrationsOpen
	}

	if isNilDBTX(s.db) {
		return ErrStorage
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id FROM outbound_deliveries WHERE state='reconciliation_required' ORDER BY id`)
	if err != nil {
		return storageError(ctx, err)
	}

	var ids []runtimecontract.DeliveryID

	for rows.Next() {
		var id runtimecontract.DeliveryID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return storageError(ctx, err)
		}

		ids = append(ids, id)
	}

	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return storageError(ctx, err)
	}

	if err := rows.Close(); err != nil {
		return storageError(ctx, err)
	}

	for _, id := range ids {
		matched, err := s.Reconcile(ctx, id, bound)

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err != nil && !recordReconciliationError(err) {
			return err
		}

		if matched && err == nil {
			// Resolve already audited the committed decision.
			continue
		}

		outcome := observability.OutcomeSkipped

		if err != nil {
			outcome = observability.OutcomeFailed
		}

		correlation, generateErr := observability.GenerateCorrelationID()
		if generateErr != nil {
			return ErrReconciliation
		}

		event, eventErr := observability.NewDiagnostic(observability.CategoryReconciliation, correlation, observability.OperationReconcile, outcome, observability.SafeFields{State: observability.StateReconciliationRequired})
		if eventErr != nil {
			return ErrReconciliation
		}

		s.diagnostics.Emit(ctx, event)
	}

	return ctx.Err()
}

func recordReconciliationError(err error) bool {
	// Fail closed on any unrecognized error: only these known record-level
	// outcomes can be isolated while readiness continues.
	for _, recordError := range []error{ErrReconciliation, ErrInvalidResolution, ErrRetryExhausted, ErrNotFound, ErrUnsupportedVersion, ErrDecode, context.DeadlineExceeded} {
		if errors.Is(err, recordError) {
			return true
		}
	}

	// Storage sentinels are intentionally not classified as record failures.
	return false
}
