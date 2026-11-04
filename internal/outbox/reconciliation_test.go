package outbox

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type reconcileFunc func(context.Context, Delivery) (ReconciliationResolution, error)

func (f reconcileFunc) Reconcile(ctx context.Context, d Delivery) (ReconciliationResolution, error) {
	return f(ctx, d)
}
func decision(d ReconciliationDecision) ReconciliationResolution {
	return ReconciliationResolution{Decision: d, CorrelationID: "0123456789abcdef0123456789abcdef"}
}

func TestReconciliationExplicitDecisions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decision ReconciliationDecision
		state    State
		reason   string
	}{
		{"confirm", ReconciliationConfirm, StateCompleted, "reconciliation_confirmed"},
		{"safe retry", ReconciliationRetry, StatePending, "reconciliation_retry"},
		{"permanent", ReconciliationPermanent, StatePermanentFailure, "reconciliation_permanent"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t, t.TempDir())
			now := time.Unix(10000, 0)
			seedRecovery(t, db, now)
			store := NewStore(db, Options{Now: func() time.Time { return now }})
			before := recoverySnapshot(t, db, "SELECT * FROM outbound_attempts ORDER BY id")
			if err := store.Resolve(context.Background(), "reconciliation", decision(tc.decision)); err != nil {
				t.Fatal(err)
			}
			status, err := store.Status(context.Background(), "reconciliation")
			if err != nil {
				t.Fatal(err)
			}
			if status.State != tc.state || status.Attempts != 2 || status.ReasonCode != tc.reason {
				t.Fatalf("resolution = %+v", status)
			}
			if tc.state == StatePending {
				if !status.NextAttemptAt.Equal(now) || !status.TerminalAt.IsZero() {
					t.Fatalf("safe retry = %+v", status)
				}
			} else if !status.TerminalAt.Equal(now) {
				t.Fatalf("terminal timestamp = %+v", status)
			}
			if got := recoverySnapshot(t, db, "SELECT * FROM outbound_attempts ORDER BY id"); !reflect.DeepEqual(got, before) {
				t.Fatal("resolution rewrote transmission facts")
			}
			if err := store.Resolve(context.Background(), "reconciliation", decision(tc.decision)); !errors.Is(err, ErrInvalidResolution) {
				t.Fatalf("duplicate resolution = %v", err)
			}
		})
	}
}

func TestReconcilerRegistrationAndMatching(t *testing.T) {
	db := openDB(t, t.TempDir())
	now := time.Unix(10000, 0)
	seedRecovery(t, db, now)
	store := NewStore(db, Options{})
	calls := 0
	handler := reconcileFunc(func(ctx context.Context, d Delivery) (ReconciliationResolution, error) {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("handler has no bound")
		}
		if d.ID != "reconciliation" || d.Payload.Version() != 1 || string(d.Payload.Data()) != "private original content" || d.Attempts != 2 {
			t.Fatalf("delivery = %+v", d)
		}
		return decision(ReconciliationConfirm), nil
	})
	if err := store.RegisterReconciler("feature", 2, handler); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterReconciler("feature", 2, handler); !errors.Is(err, ErrRegistrationConflict) {
		t.Fatalf("duplicate = %v", err)
	}
	if _, err := store.Reconcile(context.Background(), "reconciliation", time.Second); !errors.Is(err, ErrRegistrationsOpen) {
		t.Fatalf("unsealed = %v", err)
	}
	store.Seal()
	before := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id")
	if matched, err := store.Reconcile(context.Background(), "reconciliation", time.Second); err != nil || matched || calls != 0 {
		t.Fatalf("unmatched=%v %v %d", matched, err, calls)
	}
	if got := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id"); !reflect.DeepEqual(got, before) {
		t.Fatal("no handler mutated work")
	}
	if err := store.RegisterReconciler("feature", 1, handler); !errors.Is(err, ErrRegistrySealed) {
		t.Fatalf("late registration = %v", err)
	}
	other := NewStore(db, Options{})
	if err := other.RegisterReconciler("feature", 1, handler); err != nil {
		t.Fatal(err)
	}
	other.Seal()
	if matched, err := other.Reconcile(context.Background(), "reconciliation", time.Second); err != nil || !matched || calls != 1 {
		t.Fatalf("matched=%v %v %d", matched, err, calls)
	}
}

func TestReconciliationTimeoutAndHandlerFailures(t *testing.T) {
	for _, mode := range []string{"timeout", "error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			db := openDB(t, t.TempDir())
			seedRecovery(t, db, time.Unix(10000, 0))
			store := NewStore(db, Options{})
			if err := store.RegisterReconciler("feature", 1, reconcileFunc(func(ctx context.Context, _ Delivery) (ReconciliationResolution, error) {
				switch mode {
				case "timeout":
					<-ctx.Done()
					return decision(ReconciliationRetry), nil
				case "panic":
					panic("private secret")
				default:
					return ReconciliationResolution{}, errors.New("private secret")
				}
			})); err != nil {
				t.Fatal(err)
			}
			store.Seal()
			before := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id")
			bound := time.Second
			if mode == "timeout" {
				bound = time.Nanosecond
			}
			if _, err := store.Reconcile(context.Background(), "reconciliation", bound); err == nil {
				t.Fatal("handler failure accepted")
			}
			if got := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id"); !reflect.DeepEqual(got, before) {
				t.Fatal("failed reconciliation mutated work")
			}
		})
	}
}

func TestReconciliationInvalidTransitionsAndRetryBound(t *testing.T) {
	db := openDB(t, t.TempDir())
	seedRecovery(t, db, time.Unix(10000, 0))
	store := NewStore(db, Options{})
	for _, id := range []runtimecontract.DeliveryID{"pending", "retrying", "abandoned", "completed", "failed"} {
		if err := store.Resolve(context.Background(), id, decision(ReconciliationConfirm)); !errors.Is(err, ErrInvalidResolution) {
			t.Fatalf("resolve %s = %v", id, err)
		}
	}
	for _, r := range []ReconciliationResolution{{}, decision("private arbitrary state"), {Decision: ReconciliationConfirm, CorrelationID: "secret"}} {
		if err := store.Resolve(context.Background(), "reconciliation", r); err == nil {
			t.Fatal("invalid resolution accepted")
		}
	}
	if _, err := db.ExecContext(context.Background(), "UPDATE outbound_deliveries SET attempt_count=max_attempts WHERE id='reconciliation'"); err != nil {
		t.Fatal(err)
	}
	if err := store.Resolve(context.Background(), "reconciliation", decision(ReconciliationRetry)); !errors.Is(err, ErrRetryExhausted) {
		t.Fatalf("exhausted retry = %v", err)
	}
	if err := store.Resolve(context.Background(), "missing", decision(ReconciliationConfirm)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}
}

func TestConcurrentReconciliationResolvesOnce(t *testing.T) {
	db := openDB(t, t.TempDir())
	seedRecovery(t, db, time.Unix(10000, 0))
	store := NewStore(db, Options{})
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, d := range []ReconciliationDecision{ReconciliationConfirm, ReconciliationPermanent} {
		wg.Add(1)
		go func() { defer wg.Done(); results <- store.Resolve(context.Background(), "reconciliation", decision(d)) }()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrInvalidResolution) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("success=%d conflicts=%d", successes, conflicts)
	}
}

func TestReconcilerStaleDecisionCannotResolveNewAttempt(t *testing.T) {
	db := openDB(t, t.TempDir())
	now := time.Unix(10000, 0)
	seedRecovery(t, db, now)
	store := NewStore(db, Options{Now: func() time.Time { return now }})
	entered, release := make(chan struct{}), make(chan struct{})
	if err := store.RegisterReconciler("feature", 1, reconcileFunc(func(context.Context, Delivery) (ReconciliationResolution, error) {
		close(entered)
		<-release
		return decision(ReconciliationConfirm), nil
	})); err != nil {
		t.Fatal(err)
	}
	store.Seal()
	result := make(chan error, 1)
	go func() { _, err := store.Reconcile(context.Background(), "reconciliation", time.Second); result <- err }()
	<-entered
	if err := store.Resolve(context.Background(), "reconciliation", decision(ReconciliationRetry)); err != nil {
		t.Fatal(err)
	}
	// A later attempt can fail indeterminately at the same clock instant. Its
	// incremented attempt count must fence out the older reconciler decision.
	if _, err := db.ExecContext(context.Background(), `UPDATE outbound_deliveries SET state='reconciliation_required',next_attempt_at=NULL,reason_code='indeterminate',attempt_count=3 WHERE id='reconciliation'`); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-result; !errors.Is(err, ErrInvalidResolution) {
		t.Fatalf("stale decision = %v", err)
	}
	status, err := store.Status(context.Background(), "reconciliation")
	if err != nil || status.State != StateReconciliationRequired || status.Attempts != 3 {
		t.Fatalf("new attempt = %+v %v", status, err)
	}
}

func TestResolutionAuditAndRollback(t *testing.T) {
	db := openDB(t, t.TempDir())
	now := time.Unix(10000, 0)
	seedRecovery(t, db, now)
	sink := &reconciliationSink{}
	store := NewStore(db, Options{Diagnostics: sink, Now: func() time.Time { return now }})
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER reject_resolution AFTER UPDATE ON outbound_deliveries WHEN NEW.id='reconciliation' BEGIN SELECT RAISE(FAIL,'private content'); END`); err != nil {
		t.Fatal(err)
	}
	before := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id")
	if err := store.Resolve(context.Background(), "reconciliation", decision(ReconciliationConfirm)); !errors.Is(err, ErrStorage) {
		t.Fatalf("failed write = %v", err)
	}
	if len(sink.events) != 0 {
		t.Fatal("audit emitted for rolled back decision")
	}
	if got := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id"); !reflect.DeepEqual(got, before) {
		t.Fatal("failed decision partially committed")
	}
	if _, err := db.ExecContext(context.Background(), "DROP TRIGGER reject_resolution"); err != nil {
		t.Fatal(err)
	}
	if err := store.Resolve(context.Background(), "reconciliation", decision(ReconciliationConfirm)); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 1 {
		t.Fatalf("audit count = %d", len(sink.events))
	}
	event := sink.events[0]
	if event.CorrelationID != decision(ReconciliationConfirm).CorrelationID || event.Category != observability.CategoryReconciliation || event.Outcome != observability.OutcomeCompleted || event.Fields.State != "completed" {
		t.Fatalf("audit = %+v", event)
	}
}

type reconciliationSink struct{ events []observability.Diagnostic }

func (s *reconciliationSink) Emit(_ context.Context, d observability.Diagnostic) {
	s.events = append(s.events, d)
}
