package interaction

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/scheduler"
)

type dispatchHandlerFunc func(context.Context, runtimecontract.WorkScope, Event) (HandlerResult, error)

func (f dispatchHandlerFunc) Handle(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
	return f(ctx, scope, event)
}

type dispatchJobHandler struct{}

func (dispatchJobHandler) Execute(context.Context, runtimecontract.WorkScope, scheduler.Job) scheduler.JobOutcome {
	return scheduler.JobOutcome{}
}

type dispatchClock struct{ now time.Time }

func (c dispatchClock) Now() time.Time { return c.now }

type dispatchFixture struct {
	db       *persistence.DB
	registry *Registry
	outbox   *outbox.Store
	jobs     *scheduler.Service
	now      time.Time
	delivery runtimecontract.DeliveryRequest
	job      runtimecontract.JobRequest
}

func openDispatchDB(t *testing.T, dir string) *persistence.DB {
	t.Helper()
	db, err := persistence.Open(context.Background(), persistence.Options{StorageDir: dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})

	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatal(err)
	}

	return db
}

func newDispatchFixture(t *testing.T, dir string) dispatchFixture {
	t.Helper()
	db := openDispatchDB(t, dir)
	_, err := db.ExecContext(context.Background(), `CREATE TABLE dispatch_feature(value TEXT NOT NULL);
 CREATE TABLE dispatch_parent(id INTEGER PRIMARY KEY);
 CREATE TABLE dispatch_child(parent_id INTEGER REFERENCES dispatch_parent(id) DEFERRABLE INITIALLY DEFERRED)`)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	store := outbox.NewStore(db, outbox.Options{Now: func() time.Time { return now }, GenerateID: func() (runtimecontract.DeliveryID, error) { return "dispatch-delivery", nil }})
	if err := store.Register("feature", 1, func(payload runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) {
		return runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -100, Kind: runtimecontract.ChatKindSupergroup}, Content: runtimecontract.MessageContent{Text: string(payload.Data())}}, nil
	}); err != nil {
		t.Fatal(err)
	}

	store.Seal()
	jobs := scheduler.New(db, dispatchClock{now})
	if err := jobs.Register("feature", 1, dispatchJobHandler{}); err != nil {
		t.Fatal(err)
	}

	jobs.SealRegistrations()
	payload, err := runtimecontract.NewVersionedPayload(1, []byte("opaque feature result"))
	if err != nil {
		t.Fatal(err)
	}

	retry := runtimecontract.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Second, MaxDelay: time.Minute}
	return dispatchFixture{db: db, registry: NewRegistry(), outbox: store, jobs: jobs, now: now,
		delivery: runtimecontract.DeliveryRequest{Kind: "feature", Payload: payload, Retry: retry},
		job:      runtimecontract.JobRequest{ID: "dispatch-job", Type: "feature", Payload: payload, DueAt: now.Add(time.Hour), Retry: retry}}
}

func (f dispatchFixture) router() *Router {
	return NewRouter(f.registry, f.db, f.db, f.outbox, f.jobs, RouterOptions{Now: func() time.Time { return f.now }, NewCorrelationID: func() (runtimecontract.CorrelationID, error) { return "dispatch-correlation", nil }})
}

func (f dispatchFixture) effects(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
	if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO dispatch_feature(value) VALUES (?)`, "opaque feature result"); err != nil {
		return HandlerResult{}, err
	}

	if _, err := scope.EnqueueDelivery(ctx, f.delivery); err != nil {
		return HandlerResult{}, err
	}

	if _, err := scope.Schedule(ctx, f.job); err != nil {
		return HandlerResult{}, err
	}

	return HandlerResult{}, nil
}

func assertDispatchCounts(t *testing.T, db runtimecontract.DBTX, want int) {
	t.Helper()
	for _, table := range []string{"dispatch_feature", "outbound_deliveries", "scheduled_jobs", "processed_updates", "runtime_cursors"} {
		var count int

		if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}

		if count != want {
			t.Errorf("%s count = %d, want %d", table, count, want)
		}
	}
}

// Inspect the complete transaction before commit, while another connection sees none.
type inspectingCursorStore struct {
	persistence.CursorStore
	inspect func(runtimecontract.DBTX)
}

func (s inspectingCursorStore) RecordTerminal(ctx context.Context, tx runtimecontract.DBTX, outcome persistence.UpdateOutcome) error {
	if err := s.CursorStore.RecordTerminal(ctx, tx, outcome); err != nil {
		return err
	}

	s.inspect(tx)
	return nil
}

func TestDispatchCommitsDurableEffectsTogether(t *testing.T) {
	dir := t.TempDir()
	f := newDispatchFixture(t, dir)
	observer := openDispatchDB(t, dir)
	if err := f.registry.RegisterCommand("feature_action", dispatchHandlerFunc(f.effects)); err != nil {
		t.Fatal(err)
	}

	f.registry.Seal()
	router := f.router()
	inspected := false
	router.cursors = inspectingCursorStore{CursorStore: f.db, inspect: func(tx runtimecontract.DBTX) {
		inspected = true
		assertDispatchCounts(t, tx, 1)
		assertDispatchCounts(t, observer, 0)
	}}
	result, err := router.Dispatch(context.Background(), Command{Identity: testIdentity(), Name: "feature_action"})
	if err != nil {
		t.Fatalf("Dispatch() must atomically commit: %v", err)
	}

	if result.Status != persistence.UpdateCompleted || result.CorrelationID != "dispatch-correlation" || !inspected {
		t.Fatalf("outcome = %+v, inspected = %v", result, inspected)
	}

	assertDispatchCounts(t, observer, 1)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openDispatchDB(t, dir)
	assertDispatchCounts(t, reopened, 1)
	terminal, exists, err := reopened.TerminalOutcome(context.Background(), 42)
	if err != nil || !exists || terminal.Status != persistence.UpdateCompleted || terminal.CorrelationID != result.CorrelationID || !terminal.CompletedAt.Equal(f.now) {
		t.Fatalf("terminal = %+v, %v, %v", terminal, exists, err)
	}

	cursor, err := reopened.CompletedUpdate(context.Background())
	if err != nil || cursor != 42 {
		t.Fatalf("cursor = %d, %v", cursor, err)
	}

	var state, content string

	if err := reopened.QueryRowContext(context.Background(), `SELECT value FROM dispatch_feature`).Scan(&state); err != nil {
		t.Fatal(err)
	}

	if err := reopened.QueryRowContext(context.Background(), `SELECT content FROM outbound_deliveries`).Scan(&content); err != nil {
		t.Fatal(err)
	}

	if state != "opaque feature result" || content != state {
		t.Fatal("feature state or opaque delivery content changed")
	}

	var payload []byte
	var due int64

	if err := reopened.QueryRowContext(context.Background(), `SELECT payload,due_at FROM scheduled_jobs WHERE id=?`, f.job.ID).Scan(&payload, &due); err != nil {
		t.Fatal(err)
	}

	if string(payload) != state || due != f.job.DueAt.UnixMilli() {
		t.Fatal("scheduled payload or due instant changed")
	}
}

func TestDispatchRollsBackEveryEffectOnStorageFailure(t *testing.T) {
	for _, scenario := range []string{"cursor write", "commit", "schedule"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDispatchFixture(t, t.TempDir())
			if scenario == "schedule" {
				f.job.Type = "unregistered"
			}

			if scenario == "cursor write" {
				if _, err := f.db.ExecContext(context.Background(), `CREATE TRIGGER reject_cursor BEFORE INSERT ON runtime_cursors BEGIN SELECT RAISE(ABORT,'injected cursor failure'); END`); err != nil {
					t.Fatal(err)
				}
			}

			handler := dispatchHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error) {
				result, err := f.effects(ctx, scope, event)
				if err == nil && scenario == "commit" {
					_, err = scope.DBTX().ExecContext(ctx, `INSERT INTO dispatch_child(parent_id) VALUES (404)`)
				}
				return result, err
			})
			if err := f.registry.RegisterCommand("feature_action", handler); err != nil {
				t.Fatal(err)
			}

			f.registry.Seal()
			result, err := f.router().Dispatch(context.Background(), Command{Identity: testIdentity(), Name: "feature_action"})

			if scenario == "schedule" {
				// Invalid feature requests are isolated terminal failures; actual
				// storage failures below remain retryable with no completion.
				if err != nil || result.Status != persistence.UpdateHandlerFailed {
					t.Fatalf("invalid schedule outcome = %+v, %v", result, err)
				}

				for _, table := range []string{"dispatch_feature", "outbound_deliveries", "scheduled_jobs"} {
					var count int

					if err := f.db.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&count); err != nil {
						t.Fatal(err)
					}

					if count != 0 {
						t.Fatalf("%s residue = %d", table, count)
					}
				}

				cursor, err := f.db.CompletedUpdate(context.Background())
				if err != nil || cursor != 42 {
					t.Fatalf("failure cursor = %d, %v", cursor, err)
				}

				return
			}

			if err == nil {
				t.Fatal("failed transaction reported success")
			}

			if !reflect.DeepEqual(result, DispatchOutcome{}) {
				t.Fatalf("failed transaction returned outcome: %+v", result)
			}

			if scenario == "commit" {
				var txErr *persistence.TransactionError

				if !errors.As(err, &txErr) || txErr.Stage != persistence.TransactionStageCommit {
					t.Fatalf("want commit failure, got %v", err)
				}
			}

			assertDispatchCounts(t, f.db, 0)
			cursor, err := f.db.CompletedUpdate(context.Background())
			if err != nil || cursor != 0 {
				t.Fatalf("cursor advanced after failure: %d, %v", cursor, err)
			}
		})
	}
}
