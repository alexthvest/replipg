package outbox

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type deliveryFunc func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome

func (f deliveryFunc) Deliver(ctx context.Context, req runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
	return f(ctx, req)
}
func acceptedDelivery() runtimecontract.DeliveryOutcome {
	return runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryAccepted, Receipt: &runtimecontract.DeliveryReceipt{ChatID: -123, MessageID: 42}}
}
func testWorker(t *testing.T, store *Store, send deliveryFunc) *Worker {
	t.Helper()
	store.Seal()
	w, err := NewWorker(store, WorkerOptions{Delivery: send})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWorkerCommitsAttemptBeforeNetworkAndCompletion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openDB(t, dir)
	now := time.Unix(1000, 0)
	store := newTestStore(t, db, now, publicResponse())
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "delivery", nil }
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	w := testWorker(t, store, func(ctx context.Context, req runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		calls++
		status, err := store.Status(ctx, "delivery")
		if err != nil || status.State != StateAttempting || status.Attempts != 1 || !status.NextAttemptAt.IsZero() {
			t.Errorf("before send: %+v, %v", status, err)
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbound_attempts WHERE delivery_id='delivery' AND finished_at IS NULL AND outcome IS NULL AND length(correlation_id)>0`).Scan(&count); err != nil || count != 1 {
			t.Errorf("attempt before send=%d,%v", count, err)
		}
		// An independent write transaction must acquire the writer while sending.
		if err := db.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO runtime_cursors(source,last_completed_id,updated_at) VALUES('network-probe',1,1)`)
			return err
		}); err != nil {
			t.Errorf("network held transaction: %v", err)
		}
		if got, ok := req.(runtimecontract.PublicResponse); !ok || got.Content.Text != publicResponse().Content.Text {
			t.Errorf("response=%T", req)
		}
		return acceptedDelivery()
	})
	done, err := w.RunOnce(ctx)
	if err != nil || !done {
		t.Fatalf("RunOnce=%v,%v", done, err)
	}
	for range 3 {
		if done, err := w.RunOnce(ctx); done || err != nil {
			t.Fatalf("terminal wakeup=%v,%v", done, err)
		}
	}
	status, err := store.Status(ctx, "delivery")
	if err != nil || status.State != StateCompleted || !status.TerminalAt.Equal(now) {
		t.Fatalf("completion=%+v,%v", status, err)
	}
	var outcome string
	var finished int64
	if err := db.QueryRowContext(ctx, `SELECT outcome,finished_at FROM outbound_attempts WHERE delivery_id='delivery'`).Scan(&outcome, &finished); err != nil || outcome != "accepted" || finished != now.UnixMilli() {
		t.Fatalf("attempt=%q,%d,%v", outcome, finished, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openDB(t, dir)
	restarted := newTestStore(t, reopened, now, publicResponse())
	rw := testWorker(t, restarted, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		calls++
		return acceptedDelivery()
	})
	if done, err := rw.RunOnce(ctx); done || err != nil {
		t.Fatalf("restart=%v,%v", done, err)
	}
	if calls != 1 {
		t.Fatalf("sends=%d", calls)
	}
}

func TestWorkerConcurrentClaimsAndRepeatedExecutionSendOnce(t *testing.T) {
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	send := deliveryFunc(func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		calls.Add(1)
		return acceptedDelivery()
	})
	workers := []*Worker{testWorker(t, store, send), testWorker(t, store, send)}
	var wg sync.WaitGroup
	results := make(chan *Claim, 2)
	for _, w := range workers {
		wg.Go(func() {
			claim, err := w.ClaimDue(context.Background())
			if err != nil {
				t.Error(err)
			}
			results <- claim
		})
	}
	wg.Wait()
	close(results)
	var claim *Claim
	for got := range results {
		if got != nil {
			if claim != nil {
				t.Fatal("multiple claims")
			}
			claim = got
		}
	}
	if claim == nil {
		t.Fatal("no due claim")
	}
	// The claim belongs to its original worker, so use its captured owner.
	for range 2 {
		wg.Go(func() {
			err := claim.worker.ExecuteClaim(context.Background(), claim)
			if err != nil && !errors.Is(err, ErrClaimLost) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("sends=%d", calls.Load())
	}
}

func TestWorkerDueGatingAndNonacceptedSafety(t *testing.T) {
	for _, kind := range []runtimecontract.DeliveryOutcomeKind{runtimecontract.DeliveryIndeterminate, runtimecontract.DeliveryRetryableFailure, runtimecontract.DeliveryPermanentFailure, runtimecontract.DeliveryCancelled, "", runtimecontract.DeliveryAccepted} {
		t.Run(string(kind), func(t *testing.T) {
			db := openDB(t, t.TempDir())
			now := time.Unix(1000, 0)
			store := newTestStore(t, db, now, publicResponse())
			store.generateID = func() (runtimecontract.DeliveryID, error) { return "delivery", nil }
			if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
				t.Fatal(err)
			}
			store.now = func() time.Time { return now.Add(-time.Millisecond) }
			calls := 0
			w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
				calls++
				return runtimecontract.DeliveryOutcome{Kind: kind}
			})
			if claim, err := w.ClaimDue(context.Background()); claim != nil || err != nil {
				t.Fatalf("early claim=%v,%v", claim, err)
			}
			store.now = func() time.Time { return now }
			if done, err := w.RunOnce(context.Background()); done || !errors.Is(err, ErrDeliveryNotCompleted) {
				t.Fatalf("nonaccepted=%v,%v", done, err)
			}
			if done, err := w.RunOnce(context.Background()); done || err != nil {
				t.Fatalf("unsafe resend=%v,%v", done, err)
			}
			status, err := store.Status(context.Background(), "delivery")
			want := StateReconciliationRequired
			switch kind {
			case runtimecontract.DeliveryRetryableFailure:
				want = StateRetrying
			case runtimecontract.DeliveryPermanentFailure:
				want = StatePermanentFailure
			}
			if err != nil || status.State != want || status.Attempts != 1 || calls != 1 {
				t.Fatalf("safety=%+v,%d,%v", status, calls, err)
			}
		})
	}
}

func TestWorkerClaimRollbackPreventsSend(t *testing.T) {
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER reject_attempt BEFORE INSERT ON outbound_attempts BEGIN SELECT RAISE(ABORT, 'reject'); END`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		calls++
		return acceptedDelivery()
	})
	if done, err := w.RunOnce(context.Background()); done || !errors.Is(err, ErrStorage) {
		t.Fatalf("failed claim=%v,%v", done, err)
	}
	var state string
	var attempts int
	if err := db.QueryRowContext(context.Background(), `SELECT state,attempt_count FROM outbound_deliveries`).Scan(&state, &attempts); err != nil || state != "pending" || attempts != 0 || calls != 0 {
		t.Fatalf("rollback=%s,%d,%d,%v", state, attempts, calls, err)
	}
}

func TestWorkerCompletionFailureIsAtomicAndNeverResends(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_acceptance BEFORE UPDATE ON outbound_attempts BEGIN SELECT RAISE(ABORT, 'reject'); END`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		calls++
		return acceptedDelivery()
	})
	claim, err := w.ClaimDue(ctx)
	if err != nil || claim == nil {
		t.Fatalf("claim=%v,%v", claim, err)
	}
	copied := *claim
	if err := w.ExecuteClaim(ctx, claim); !errors.Is(err, ErrStorage) {
		t.Fatalf("acceptance commit=%v", err)
	}
	if err := w.ExecuteClaim(ctx, &copied); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("copied execution=%v", err)
	}
	var state string
	var unfinished int
	if err := db.QueryRowContext(ctx, `SELECT state FROM outbound_deliveries`).Scan(&state); err != nil || state != "attempting" {
		t.Fatalf("partial completion=%s,%v", state, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM outbound_attempts WHERE finished_at IS NULL AND outcome IS NULL`).Scan(&unfinished); err != nil || unfinished != 1 {
		t.Fatalf("partial attempt=%d,%v", unfinished, err)
	}
	if done, err := w.RunOnce(ctx); done || err != nil || calls != 1 {
		t.Fatalf("wake after failed commit=%v,%v,calls=%d", done, err, calls)
	}
}

func TestWorkerRejectsStaleClaimAndCancellation(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "stale", true: "cancelled"}[cancel], func(t *testing.T) {
			ctx := context.Background()
			db := openDB(t, t.TempDir())
			store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
			if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
				t.Fatal(err)
			}
			calls := 0
			w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
				calls++
				return acceptedDelivery()
			})
			claim, err := w.ClaimDue(ctx)
			if err != nil || claim == nil {
				t.Fatalf("claim=%v,%v", claim, err)
			}
			expected := ErrClaimLost
			if cancel {
				var stop context.CancelFunc
				ctx, stop = context.WithCancel(ctx)
				stop()
				expected = context.Canceled
			} else {
				if _, err := db.ExecContext(ctx, `UPDATE outbound_deliveries SET state='reconciliation_required',reason_code='interrupted'`); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.ExecuteClaim(ctx, claim); !errors.Is(err, expected) {
				t.Fatalf("execution=%v", err)
			}
			if calls != 0 {
				t.Fatalf("unsafe calls=%d", calls)
			}
		})
	}
}

func TestWorkerRetryDueAndTerminalEligibility(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, t.TempDir())
	now := time.Unix(1000, 0)
	store := newTestStore(t, db, now, publicResponse())
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "delivery", nil }
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE outbound_deliveries SET state='retrying',attempt_count=1,next_attempt_at=?`, now.Add(time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	calls := 0
	w := testWorker(t, store, func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		calls++
		return acceptedDelivery()
	})
	if claim, err := w.ClaimDue(ctx); claim != nil || err != nil {
		t.Fatalf("early retry=%v,%v", claim, err)
	}
	store.now = func() time.Time { return now.Add(time.Second) }
	if done, err := w.RunOnce(ctx); !done || err != nil || calls != 1 {
		t.Fatalf("due retry=%v,%v,%d", done, err, calls)
	}
	if _, err := db.ExecContext(ctx, `UPDATE outbound_deliveries SET state='permanent_failure',reason_code='rejected'`); err != nil {
		t.Fatal(err)
	}
	if claim, err := w.ClaimDue(ctx); claim != nil || err != nil {
		t.Fatalf("permanent claim=%v,%v", claim, err)
	}
}

func TestWorkerRequiresSealedRegistryAndValidDependencies(t *testing.T) {
	send := deliveryFunc(func(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
		return acceptedDelivery()
	})
	if _, err := NewWorker(nil, WorkerOptions{Delivery: send}); !errors.Is(err, ErrInvalidWorker) {
		t.Fatalf("nil store=%v", err)
	}
	store := newTestStore(t, openDB(t, t.TempDir()), time.Unix(1000, 0), publicResponse())
	if _, err := NewWorker(store, WorkerOptions{}); !errors.Is(err, ErrInvalidWorker) {
		t.Fatalf("nil delivery=%v", err)
	}
	var nilPort deliveryFunc
	if _, err := NewWorker(store, WorkerOptions{Delivery: nilPort}); !errors.Is(err, ErrInvalidWorker) {
		t.Fatalf("typed nil delivery=%v", err)
	}
	w, err := NewWorker(store, WorkerOptions{Delivery: send})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ClaimDue(context.Background()); !errors.Is(err, ErrRegistrationsOpen) {
		t.Fatalf("open registry=%v", err)
	}
}
