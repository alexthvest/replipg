package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestReconcilePendingRecoveredDecisions(t *testing.T) {
	db := openDB(t, t.TempDir())
	now := time.Unix(10000, 0)
	seedRecovery(t, db, now)
	store := NewStore(db, Options{Now: func() time.Time { return now }})
	calls := map[string]int{}

	if err := store.RegisterReconciler("feature", 1, reconcileFunc(func(ctx context.Context, d Delivery) (ReconciliationResolution, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing handler bound")
		}

		if string(d.Payload.Data()) != "private original content" || d.Attempts != 2 {
			t.Fatal("recovered facts changed")
		}

		calls[string(d.ID)]++

		switch d.ID {
		case "abandoned":
			return decision(ReconciliationConfirm), nil
		case "possibly-transmitted":
			return decision(ReconciliationRetry), nil
		default:
			return decision(ReconciliationPermanent), nil
		}
	})); err != nil {
		t.Fatal(err)
	}

	store.Seal()

	if err := store.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := store.ReconcilePending(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string]State{"abandoned": StateCompleted, "possibly-transmitted": StatePending, "reconciliation": StatePermanentFailure} {
		status, err := store.Status(context.Background(), runtimecontract.DeliveryID(id))
		if err != nil || status.State != want || calls[id] != 1 {
			t.Fatalf("%s = %+v, calls=%d err=%v", id, status, calls[id], err)
		}
	}

	if err := store.ReconcilePending(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}

	for id, count := range calls {
		if count != 1 {
			t.Fatalf("processed %s %d times", id, count)
		}
	}
}

func TestReconcilePendingIsolationSnapshotAndUnmatched(t *testing.T) {
	for _, failure := range []string{"error", "timeout", "invalid"} {
		t.Run(failure, func(t *testing.T) {
			db := openDB(t, t.TempDir())
			now := time.Unix(10000, 0)
			seedRecovery(t, db, now)
			sink := &reconciliationSink{}
			store := NewStore(db, Options{Diagnostics: sink})

			if err := store.Recover(context.Background()); err != nil {
				t.Fatal(err)
			}

			if _, err := db.ExecContext(context.Background(), `UPDATE outbound_deliveries SET kind='unmatched' WHERE id='reconciliation'`); err != nil {
				t.Fatal(err)
			}

			calls := map[string]int{}

			if err := store.RegisterReconciler("feature", 1, reconcileFunc(func(ctx context.Context, d Delivery) (ReconciliationResolution, error) {
				calls[string(d.ID)]++

				if d.ID == "abandoned" {
					// New work created during this pass must wait for the next startup pass.
					if _, err := db.ExecContext(context.Background(), `INSERT INTO outbound_deliveries SELECT 'new-record',kind,content_version,content,destination_chat_id,target_user_id,authorization_kind,callback_query_id,incoming_ephemeral_message_id,authorization_expires_at,replace_callback_message,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,next_attempt_at,reason_code,created_at,updated_at,terminal_at FROM outbound_deliveries WHERE id='abandoned'`); err != nil {
						t.Fatal(err)
					}

					switch failure {
					case "timeout":
						<-ctx.Done()
						return decision(ReconciliationConfirm), nil
					case "invalid":
						return ReconciliationResolution{}, nil
					default:
						return ReconciliationResolution{}, errors.New("private secret error")
					}
				}

				return decision(ReconciliationConfirm), nil
			})); err != nil {
				t.Fatal(err)
			}

			store.Seal()
			// Wait for cancellation without relying on elapsed wall-clock assertions.
			bound := time.Second

			if failure == "timeout" {
				bound = 50 * time.Millisecond
			}

			if err := store.ReconcilePending(context.Background(), bound); err != nil {
				t.Fatal(err)
			}

			if calls["abandoned"] != 1 || calls["possibly-transmitted"] != 1 || calls["new-record"] != 0 || calls["reconciliation"] != 0 {
				t.Fatalf("calls = %v", calls)
			}

			for _, id := range []string{"abandoned", "new-record", "reconciliation"} {
				status, err := store.Status(context.Background(), runtimecontract.DeliveryID(id))
				if err != nil || status.State != StateReconciliationRequired {
					t.Fatalf("%s = %+v %v", id, status, err)
				}
			}

			status, err := store.Status(context.Background(), "possibly-transmitted")
			if err != nil || status.State != StateCompleted {
				t.Fatalf("later record = %+v %v", status, err)
			}

			failed, skipped := 0, 0

			for _, event := range sink.events {
				if event.Category != observability.CategoryReconciliation || len(event.CorrelationID) != 32 {
					t.Fatalf("unsafe audit = %+v", event)
				}

				if event.Outcome == observability.OutcomeFailed {
					failed++
				}

				if event.Outcome == observability.OutcomeSkipped {
					skipped++
				}
			}

			if failed != 1 || skipped != 1 {
				t.Fatalf("failure diagnostics=%d unmatched=%d", failed, skipped)
			}
		})
	}
}

func TestReconcilePendingCancellationAndStorageFatal(t *testing.T) {
	db := openDB(t, t.TempDir())
	seedRecovery(t, db, time.Unix(10000, 0))
	store := NewStore(db, Options{})
	ctx, cancel := context.WithCancel(context.Background())

	if err := store.RegisterReconciler("feature", 1, reconcileFunc(func(context.Context, Delivery) (ReconciliationResolution, error) {
		cancel()
		return decision(ReconciliationRetry), nil
	})); err != nil {
		t.Fatal(err)
	}

	store.Seal()

	if err := store.ReconcilePending(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}

	status, err := store.Status(context.Background(), "reconciliation")
	if err != nil || status.State != StateReconciliationRequired {
		t.Fatalf("cancelled work = %+v %v", status, err)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := store.ReconcilePending(context.Background(), time.Second); !errors.Is(err, persistence.ErrPersistenceClosed) {
		t.Fatalf("closed store = %v", err)
	}
}

func TestReconcilePendingStorageFailureDuringResolution(t *testing.T) {
	db := openDB(t, t.TempDir())
	seedRecovery(t, db, time.Unix(10000, 0))
	store := NewStore(db, Options{})

	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER fail_pending_resolution BEFORE UPDATE ON outbound_deliveries WHEN NEW.id='reconciliation' BEGIN SELECT RAISE(FAIL,'private storage secret'); END`); err != nil {
		t.Fatal(err)
	}

	if err := store.RegisterReconciler("feature", 1, reconcileFunc(func(context.Context, Delivery) (ReconciliationResolution, error) {
		return decision(ReconciliationConfirm), nil
	})); err != nil {
		t.Fatal(err)
	}

	store.Seal()

	if err := store.ReconcilePending(context.Background(), time.Second); !errors.Is(err, ErrStorage) {
		t.Fatalf("storage failure must stop startup: %v", err)
	}

	status, err := store.Status(context.Background(), "reconciliation")
	if err != nil || status.State != StateReconciliationRequired {
		t.Fatalf("failed resolution = %+v %v", status, err)
	}
}

func TestReconcilePendingValidatesSetup(t *testing.T) {
	store := NewStore(nil, Options{})

	if err := store.ReconcilePending(context.Background(), 0); !errors.Is(err, ErrInvalidResolution) {
		t.Fatalf("zero bound = %v", err)
	}

	if err := store.ReconcilePending(context.Background(), time.Second); !errors.Is(err, ErrRegistrationsOpen) {
		t.Fatalf("unsealed = %v", err)
	}

	store.Seal()

	if err := store.ReconcilePending(context.Background(), time.Second); !errors.Is(err, ErrStorage) {
		t.Fatalf("nil storage = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.ReconcilePending(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled before pass = %v", err)
	}
}
