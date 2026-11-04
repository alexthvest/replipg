package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestWorkerBoundedRetrySchedule(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, t.TempDir())
	now := time.Unix(1000, 123456)
	store := newTestStore(t, db, now, publicResponse())
	store.now = func() time.Time { return now }
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "delivery", nil }
	req := deliveryRequest(t, 1)
	req.Retry.MaxAttempts = 5
	req.Retry.MaxDelay = 2500 * time.Millisecond
	if err := enqueue(t, db, store, req); err != nil {
		t.Fatal(err)
	}

	// Enqueue truncates the initial eligibility time; subsequent retries must round up.
	calls := 0
	w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		calls++
		after := time.Duration(0)
		if calls == 2 {
			after = 7*time.Second + time.Nanosecond
		}
		return runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryRetryableFailure, RetryAfter: after, ReasonCode: "private payload"}
	})
	for i, delay := range []time.Duration{time.Second, 7*time.Second + time.Nanosecond, 2500 * time.Millisecond, 2500 * time.Millisecond, 0} {
		start := now
		if done, err := w.RunOnce(ctx); done || !errors.Is(err, ErrDeliveryNotCompleted) {
			t.Fatalf("attempt %d=%v,%v", i+1, done, err)
		}

		status, err := store.Status(ctx, "delivery")
		if err != nil {
			t.Fatal(err)
		}

		if status.Attempts != i+1 {
			t.Fatalf("attempts=%d", status.Attempts)
		}

		if i == 4 {
			if status.State != StatePermanentFailure || status.ReasonCode != "retry_exhausted" || status.TerminalAt.IsZero() {
				t.Fatalf("exhaustion=%+v", status)
			}
		} else {
			expected := start.Add(delay).Truncate(time.Millisecond)
			if expected.Before(start.Add(delay)) {
				expected = expected.Add(time.Millisecond)
			}

			if status.State != StateRetrying || !status.NextAttemptAt.Equal(expected) || status.ReasonCode != "delivery_failed" {
				t.Fatalf("retry=%+v want due %v", status, expected)
			}

			now = expected.Add(-time.Nanosecond)
			if claim, err := w.ClaimDue(ctx); claim != nil || err != nil {
				t.Fatalf("early=%v,%v", claim, err)
			}

			now = expected
		}
	}

	for range 2 {
		if claim, err := w.ClaimDue(ctx); claim != nil || err != nil {
			t.Fatalf("exhausted claim=%v,%v", claim, err)
		}
	}

	var finished int

	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbound_attempts WHERE finished_at IS NOT NULL AND reason_code NOT LIKE '%private%'`).Scan(&finished); err != nil || finished != 5 {
		t.Fatalf("finished=%d,%v", finished, err)
	}
}

func TestWorkerDefinitiveAndUncertainFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome runtimecontract.DeliveryOutcome
		state   State
		reason  string
	}{
		{"permanent", runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryPermanentFailure, ReasonCode: "authorization"}, StatePermanentFailure, "authorization"},
		{"unsafe permanent", runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryPermanentFailure, ReasonCode: "private secret"}, StatePermanentFailure, "delivery_failed"},
		{"indeterminate", runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryIndeterminate}, StateReconciliationRequired, "indeterminate"},
		{"unknown", runtimecontract.DeliveryOutcome{}, StateReconciliationRequired, "indeterminate"},
		{"cancelled", runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryCancelled}, StateReconciliationRequired, "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t, t.TempDir())
			store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
			store.generateID = func() (runtimecontract.DeliveryID, error) { return "delivery", nil }
			if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
				t.Fatal(err)
			}

			w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
				return tc.outcome
			})
			if _, err := w.RunOnce(context.Background()); !errors.Is(err, ErrDeliveryNotCompleted) {
				t.Fatal(err)
			}

			status, err := store.Status(context.Background(), "delivery")
			if err != nil || status.State != tc.state || status.ReasonCode != tc.reason {
				t.Fatalf("status=%+v,%v", status, err)
			}

			if claim, err := w.ClaimDue(context.Background()); claim != nil || err != nil {
				t.Fatalf("resend=%v,%v", claim, err)
			}
		})
	}
}

func TestWorkerWaitNextUsesDueTimeAndCancellation(t *testing.T) {
	db := openDB(t, t.TempDir())
	now := time.Unix(1000, 0)
	store := newTestStore(t, db, now, publicResponse())
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}

	store.Seal()
	var waited time.Duration

	w, err := NewWorker(store, WorkerOptions{Delivery: deliveryFunc(func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		return acceptedDelivery()
	}), Sleep: func(ctx context.Context, d time.Duration) error { waited = d; return ctx.Err() }})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(context.Background(), `UPDATE outbound_deliveries SET next_attempt_at=?`, now.Add(2*time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	if err := w.WaitNext(context.Background(), time.Hour); err != nil || waited != 2*time.Second {
		t.Fatalf("wait=%v,%v", waited, err)
	}

	if err := w.WaitNext(context.Background(), time.Second); err != nil || waited != time.Second {
		t.Fatalf("bounded wait=%v,%v", waited, err)
	}

	w.options.Sleep = sleepContext
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := w.WaitNext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestWorkerExpiredAuthorizationFailsBeforeIO(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, t.TempDir())
	now := time.Unix(1000, 0)
	response := runtimecontract.EphemeralResponse{Destination: runtimecontract.GroupDestination{ChatID: -123, Kind: runtimecontract.ChatKindGroup}, TargetUserID: 42, Content: runtimecontract.MessageContent{Text: "private"}, Authorization: runtimecontract.CallbackAuthorization{CallbackQueryID: "secret", ValidUntil: now.Add(time.Second)}}
	store := newTestStore(t, db, now, response)
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "delivery", nil }
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}

	store.now = func() time.Time { return now.Add(time.Second) }
	calls := 0
	w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		calls++
		return acceptedDelivery()
	})
	if _, err := w.RunOnce(ctx); !errors.Is(err, ErrDeliveryNotCompleted) {
		t.Fatal(err)
	}

	status, err := store.Status(ctx, "delivery")
	if err != nil || status.State != StatePermanentFailure || status.ReasonCode != "authorization_expired" || calls != 0 {
		t.Fatalf("expired=%+v,calls=%d,%v", status, calls, err)
	}
}

func TestWorkerCancellationPreservesAttempt(t *testing.T) {
	db := openDB(t, t.TempDir())
	now := time.Unix(1000, 0)
	store := newTestStore(t, db, now, publicResponse())
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "delivery", nil }
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		calls++
		cancel()
		return runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryRetryableFailure}
	})
	if _, err := w.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delivery=%v", err)
	}

	status, err := store.Status(context.Background(), "delivery")
	if err != nil || status.State != StateAttempting || status.Attempts != 1 {
		t.Fatalf("cancelled status=%+v,%v", status, err)
	}

	if err := store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}

	if claim, err := w.ClaimDue(context.Background()); claim != nil || err != nil || calls != 1 {
		t.Fatalf("blind resend=%v,%v,calls=%d", claim, err, calls)
	}
}

func TestRetryTransitionRollbackPreservesAttempt(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "delivery", nil }
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_retry BEFORE UPDATE ON outbound_attempts BEGIN SELECT RAISE(ABORT, 'private'); END`); err != nil {
		t.Fatal(err)
	}

	w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		return runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryRetryableFailure}
	})
	if _, err := w.RunOnce(ctx); !errors.Is(err, ErrStorage) {
		t.Fatalf("transition=%v", err)
	}

	status, err := store.Status(ctx, "delivery")
	if err != nil || status.State != StateAttempting || status.Attempts != 1 {
		t.Fatalf("rollback=%+v,%v", status, err)
	}

	var unfinished int

	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbound_attempts WHERE finished_at IS NULL AND outcome IS NULL`).Scan(&unfinished); err != nil || unfinished != 1 {
		t.Fatalf("unfinished=%d,%v", unfinished, err)
	}
}

func TestSleepContextInterruptsLongWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result := make(chan error, 1)
	go func() { result <- sleepContext(ctx, time.Hour) }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("retry wait ignored cancellation")
	}
}

func TestRetryDelayMaximumDuration(t *testing.T) {
	maximum := time.Duration(1<<63 - 1)
	ms := durationMilliseconds(maximum)
	if got := retryDelayMillis(ms/2+1, ms, 1<<30); got != ms {
		t.Fatalf("overflow delay=%d want %d", got, ms)
	}

	now := time.Unix(1000, 0)
	due := now.Add(time.Duration(ms/1000) * time.Second).Add(time.Duration(ms%1000) * time.Millisecond)

	if due.Before(now.Add(maximum)) {
		t.Fatalf("maximum delay wrapped: %v", due)
	}
}
