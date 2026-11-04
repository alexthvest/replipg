package interaction

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type diagnosticCapture struct{ events []observability.Diagnostic }

func (s *diagnosticCapture) Emit(_ context.Context, d observability.Diagnostic) {
	s.events = append(s.events, d)
}

func TestDispatchIsolatesHandlerFailures(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panics], func(t *testing.T) {
			f := newDispatchFixture(t, t.TempDir())
			calls := 0
			if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
				calls++
				if calls > 1 {
					return HandlerResult{}, nil
				}
				if _, err := f.effects(ctx, scope, event); err != nil {
					return HandlerResult{}, err
				}
				if panics {
					panic("secret panic content")
				}
				return HandlerResult{}, errors.New("secret feature content")
			})); err != nil {
				t.Fatal(err)
			}
			f.registry.Seal()
			r := f.router()
			sink := &diagnosticCapture{}
			r.options.Diagnostics = sink
			event := Command{Identity: testIdentity(), Name: "feature_action"}
			outcome, err := r.Dispatch(context.Background(), event)
			if err != nil || outcome.Status != persistence.UpdateHandlerFailed {
				t.Fatalf("failure outcome = %+v, %v", outcome, err)
			}
			for _, table := range []string{"dispatch_feature", "outbound_deliveries", "scheduled_jobs"} {
				var count int
				if err := f.db.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("%s residue = %d", table, count)
				}
			}
			record, exists, err := f.db.TerminalOutcome(context.Background(), 42)
			if err != nil || !exists || record.Status != persistence.UpdateHandlerFailed || record.FailureCode == "" {
				t.Fatalf("record = %+v %v %v", record, exists, err)
			}
			if len(sink.events) != 1 || sink.events[0].CorrelationID != outcome.CorrelationID || sink.events[0].Outcome != observability.OutcomeFailed {
				t.Fatalf("diagnostics = %+v", sink.events)
			}
			if _, err := r.Dispatch(context.Background(), event); err != nil || calls != 1 || len(sink.events) != 1 {
				t.Fatalf("replay err=%v calls=%d diagnostics=%d", err, calls, len(sink.events))
			}
			event.Update = 43
			outcome, err = r.Dispatch(context.Background(), event)
			if err != nil || outcome.Status != persistence.UpdateCompleted || calls != 2 {
				t.Fatalf("next = %+v %v calls=%d", outcome, err, calls)
			}
		})
	}
}

func TestDispatchSkipsUnownedAndDecodedFailures(t *testing.T) {
	f := newDispatchFixture(t, t.TempDir())
	f.registry.Seal()
	r := f.router()
	outcome, err := r.Dispatch(context.Background(), Command{Identity: testIdentity(), Name: "unknown"})
	if err != nil || outcome.Status != persistence.UpdateIgnored {
		t.Fatalf("ignored = %+v %v", outcome, err)
	}
	sink := &diagnosticCapture{}
	r.options.Diagnostics = sink
	outcome, err = r.RecordDecodeFailure(context.Background(), 43)
	if err != nil || outcome.Status != persistence.UpdateDecodeFailed || len(sink.events) != 1 {
		t.Fatalf("decode = %+v %v %+v", outcome, err, sink.events)
	}
	if _, err := r.RecordIgnored(context.Background(), 44); err != nil {
		t.Fatal(err)
	}
	if cursor, err := f.db.CompletedUpdate(context.Background()); err != nil || cursor != 44 {
		t.Fatalf("cursor = %d %v", cursor, err)
	}
}

func TestDispatchFailureRecordingIsAtomic(t *testing.T) {
	f := newDispatchFixture(t, t.TempDir())
	if _, err := f.db.ExecContext(context.Background(), `CREATE TRIGGER reject_failure_cursor BEFORE INSERT ON runtime_cursors BEGIN SELECT RAISE(ABORT,'secret storage detail'); END`); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
		if _, err := f.effects(ctx, scope, event); err != nil {
			return HandlerResult{}, err
		}
		return HandlerResult{}, errors.New("secret failure")
	})); err != nil {
		t.Fatal(err)
	}
	f.registry.Seal()
	result, err := f.router().Dispatch(context.Background(), Command{Identity: testIdentity(), Name: "feature_action"})
	if err == nil || !reflect.DeepEqual(result, DispatchOutcome{}) {
		t.Fatalf("unsafe failure = %+v %v", result, err)
	}
	assertDispatchCounts(t, f.db, 0)
}

func TestDispatchCancellationAndDatabaseFailuresStayRetryable(t *testing.T) {
	for _, scenario := range []string{"cancel", "write", "swallowed write", "read"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDispatchFixture(t, t.TempDir())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, _ Event) (HandlerResult, error) {
				switch scenario {
				case "cancel":
					cancel()
					return HandlerResult{}, errors.New("feature failure after cancellation")
				case "read":
					var value string
					err := scope.DBTX().QueryRowContext(ctx, `SELECT value FROM absent_table`).Scan(&value)
					return HandlerResult{}, err
				default:
					_, err := scope.DBTX().ExecContext(ctx, `INSERT INTO absent_table VALUES (1)`)
					if scenario == "swallowed write" {
						return HandlerResult{}, nil
					}
					return HandlerResult{}, err
				}
			})); err != nil {
				t.Fatal(err)
			}
			f.registry.Seal()
			r := f.router()
			sink := &diagnosticCapture{}
			r.options.Diagnostics = sink
			result, err := r.Dispatch(ctx, Command{Identity: testIdentity(), Name: "feature_action"})
			if err == nil || !reflect.DeepEqual(result, DispatchOutcome{}) || len(sink.events) != 0 {
				t.Fatalf("retryable failure = %+v %v diagnostics=%+v", result, err, sink.events)
			}
			assertDispatchCounts(t, f.db, 0)
		})
	}
}

func TestDispatchCompletedReplayDoesNotInvokeHandler(t *testing.T) {
	f := newDispatchFixture(t, t.TempDir())
	calls := 0
	if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
		calls++
		return f.effects(ctx, scope, event)
	})); err != nil {
		t.Fatal(err)
	}
	f.registry.Seal()
	r := f.router()
	event := Command{Identity: testIdentity(), Name: "feature_action"}
	first, err := r.Dispatch(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	r.options.NewCorrelationID = func() (runtimecontract.CorrelationID, error) { return "second-correlation", nil }
	replay, err := r.Dispatch(context.Background(), event)
	if err != nil || !reflect.DeepEqual(first, replay) || calls != 1 {
		t.Fatalf("replay=%+v first=%+v err=%v calls=%d", replay, first, err, calls)
	}
	event.Update = 41
	stale, err := r.Dispatch(context.Background(), event)
	if err != nil || stale.Status != persistence.UpdateIgnored || calls != 1 {
		t.Fatalf("stale=%+v err=%v calls=%d", stale, err, calls)
	}
	assertDispatchCounts(t, f.db, 1)
}

type rollbackFailureTransactor struct{ delegate persistence.Transactor }

func (s rollbackFailureTransactor) WithinTransaction(ctx context.Context, fn func(context.Context, runtimecontract.DBTX) error) error {
	err := s.delegate.WithinTransaction(ctx, fn)
	if err != nil {
		return &persistence.TransactionError{Stage: persistence.TransactionStageRollback}
	}
	return nil
}

func TestDispatchUnsafeRollbackDoesNotRecordFailure(t *testing.T) {
	f := newDispatchFixture(t, t.TempDir())
	if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
		if _, err := f.effects(ctx, scope, event); err != nil {
			return HandlerResult{}, err
		}
		return HandlerResult{}, errors.New("feature failure")
	})); err != nil {
		t.Fatal(err)
	}
	f.registry.Seal()
	r := f.router()
	r.transactions = rollbackFailureTransactor{delegate: f.db}
	result, err := r.Dispatch(context.Background(), Command{Identity: testIdentity(), Name: "feature_action"})
	if err == nil || !reflect.DeepEqual(result, DispatchOutcome{}) {
		t.Fatalf("unsafe rollback=%+v %v", result, err)
	}
	assertDispatchCounts(t, f.db, 0)
}

func TestDispatchConcurrentDuplicateProcessesOnce(t *testing.T) {
	f := newDispatchFixture(t, t.TempDir())
	if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(f.effects)); err != nil {
		t.Fatal(err)
	}
	f.registry.Seal()
	r := f.router()
	results := make(chan error, 2)
	for range 2 {
		go func() {
			outcome, err := r.Dispatch(context.Background(), Command{Identity: testIdentity(), Name: "feature_action"})
			if err == nil && outcome.Status != persistence.UpdateCompleted {
				err = errors.New("wrong duplicate status")
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	assertDispatchCounts(t, f.db, 1)
}

func TestDispatchInvalidDeliveryRequestIsHandlerFailure(t *testing.T) {
	f := newDispatchFixture(t, t.TempDir())
	if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, _ Event) (HandlerResult, error) {
		if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO dispatch_feature(value) VALUES ('rolled back')`); err != nil {
			return HandlerResult{}, err
		}
		f.delivery.Kind = "unregistered"
		_, err := scope.EnqueueDelivery(ctx, f.delivery)
		return HandlerResult{}, err
	})); err != nil {
		t.Fatal(err)
	}
	f.registry.Seal()
	r := f.router()
	sink := &diagnosticCapture{}
	r.options.Diagnostics = sink
	r.options.NewCorrelationID = func() (runtimecontract.CorrelationID, error) { return "0123456789abcdef0123456789abcdef", nil }
	result, err := r.Dispatch(context.Background(), Command{Identity: testIdentity(), Name: "feature_action"})
	if err != nil || result.Status != persistence.UpdateHandlerFailed || len(sink.events) != 1 {
		t.Fatalf("invalid request = %+v %v diagnostics=%+v", result, err, sink.events)
	}
	for _, table := range []string{"dispatch_feature", "outbound_deliveries", "scheduled_jobs"} {
		var n int
		if err := f.db.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s residue=%d", table, n)
		}
	}
}
