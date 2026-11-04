package outbox

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// WaitNext waits until eligible work is due or maxWait elapses. The caller owns
// repeated invocation and uses maxWait to discover newly enqueued work. It owns
// no goroutine; cancellation stops the sole timer promptly.
func (w *Worker) WaitNext(ctx context.Context, maxWait time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if maxWait <= 0 {
		return ErrInvalidWorker
	}

	var next sql.NullInt64

	if err := w.store.db.QueryRowContext(ctx, `SELECT min(next_attempt_at) FROM outbound_deliveries WHERE state IN ('pending','retrying') AND attempt_count<max_attempts`).Scan(&next); err != nil {
		return workerStorageError(ctx, err)
	}

	delay := maxWait
	if next.Valid {
		delay = min(delay, max(time.Duration(0), time.UnixMilli(next.Int64).Sub(w.store.now())))
	}
	if delay == 0 {
		return ctx.Err()
	}

	return w.options.Sleep(ctx, delay)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

func (w *Worker) recordFailure(ctx context.Context, claim *Claim, outcome runtimecontract.DeliveryOutcome) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	err := w.tx.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var attempts, bound int
		var initial, maximum int64

		err := tx.QueryRowContext(ctx, `SELECT attempt_count,max_attempts,initial_delay_ms,max_delay_ms FROM outbound_deliveries WHERE id=? AND state='attempting' AND EXISTS(SELECT 1 FROM outbound_attempts WHERE id=? AND delivery_id=? AND finished_at IS NULL)`, claim.id, claim.attemptID, claim.id).Scan(&attempts, &bound, &initial, &maximum)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrClaimLost
		}

		if err != nil {
			return err
		}

		now := w.store.now().UTC()
		state, reason := StateReconciliationRequired, "indeterminate"
		attemptOutcome := runtimecontract.DeliveryIndeterminate
		var next, terminal any

		switch outcome.Kind {
		case runtimecontract.DeliveryRetryableFailure:
			attemptOutcome = runtimecontract.DeliveryRetryableFailure
			reason = safeReason(outcome.ReasonCode)
			if attempts >= bound {
				state, reason, terminal = StatePermanentFailure, "retry_exhausted", now.UnixMilli()
			} else {
				state = StateRetrying
				// Persisted policy milliseconds are bounded by time.Duration. Convert the
				// rounded delay without overflowing even at its maximum representable value.
				delay := retryDelayMillis(initial, maximum, attempts)
				due := now.Add(time.Duration(delay/1000) * time.Second).Add(time.Duration(delay%1000) * time.Millisecond)
				if floor := now.Add(outcome.RetryAfter); floor.After(due) {
					due = floor
				}
				next = ceilRetryTime(due)
			}
		case runtimecontract.DeliveryPermanentFailure:
			state, reason, terminal = StatePermanentFailure, safeReason(outcome.ReasonCode), now.UnixMilli()
			attemptOutcome = runtimecontract.DeliveryPermanentFailure
		case runtimecontract.DeliveryCancelled:
			reason = "cancelled"
			attemptOutcome = runtimecontract.DeliveryCancelled
		}

		result, err := tx.ExecContext(ctx, `UPDATE outbound_deliveries SET state=?,next_attempt_at=?,reason_code=?,updated_at=?,terminal_at=? WHERE id=? AND state='attempting' AND EXISTS(SELECT 1 FROM outbound_attempts WHERE id=? AND delivery_id=? AND finished_at IS NULL)`, state, next, reason, now.UnixMilli(), terminal, claim.id, claim.attemptID, claim.id)
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

		result, err = tx.ExecContext(ctx, `UPDATE outbound_attempts SET finished_at=MAX(?,started_at),outcome=?,reason_code=? WHERE id=? AND delivery_id=? AND finished_at IS NULL`, now.UnixMilli(), attemptOutcome, reason, claim.attemptID, claim.id)
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
	if err != nil {
		return workerStorageError(ctx, err)
	}

	return ErrDeliveryNotCompleted
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

func ceilRetryTime(at time.Time) int64 {
	ms := at.UnixMilli()
	if at.Nanosecond()%int(time.Millisecond) != 0 {
		ms++
	}
	return ms
}
