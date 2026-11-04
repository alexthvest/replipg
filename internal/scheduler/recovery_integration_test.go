package scheduler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/scheduler"
	"github.com/alexthvest/replipg/internal/testsupport"
)

// Only time and feature behavior are controlled. Durable effects use the real
// outbox and scheduler stores bound to the worker's SQLite transaction.
type scheduledScope struct {
	tx         runtimecontract.DBTX
	jobs       *scheduler.Service
	deliveries *outbox.Store
}

func (s scheduledScope) DBTX() runtimecontract.DBTX { return s.tx }

func (s scheduledScope) Schedule(ctx context.Context, r runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	return s.jobs.Schedule(ctx, s.tx, r)
}

func (s scheduledScope) EnqueueDelivery(ctx context.Context, r runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	return s.deliveries.Enqueue(ctx, s.tx, r)
}

type scheduledHandler func(context.Context, runtimecontract.WorkScope, scheduler.Job) scheduler.JobOutcome

func (f scheduledHandler) Execute(ctx context.Context, s runtimecontract.WorkScope, j scheduler.Job) scheduler.JobOutcome {
	return f(ctx, s, j)
}

type scheduledRecovery struct {
	t          *testing.T
	dir        string
	db         *persistence.DB
	jobs       *scheduler.Service
	deliveries *outbox.Store
	worker     *scheduler.Worker
	clock      *testsupport.ManualClock
	handler    scheduledHandler
}

func newScheduledRecovery(t *testing.T, handler scheduledHandler) *scheduledRecovery {
	t.Helper()
	h := &scheduledRecovery{t: t, dir: t.TempDir(), clock: testsupport.NewManualClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)), handler: handler}
	t.Cleanup(func() {
		if h.db != nil {
			if err := h.db.Close(); err != nil {
				t.Error(err)
			}
		}
	})

	h.restart()
	h.exec(`CREATE TABLE scheduled_feature (job_id TEXT PRIMARY KEY, value TEXT NOT NULL)`)
	return h
}

func (h *scheduledRecovery) restart() {
	h.t.Helper()
	if h.db != nil {
		if err := h.db.Close(); err != nil {
			h.t.Fatal(err)
		}
	}
	var err error

	h.db, err = persistence.Open(context.Background(), persistence.Options{StorageDir: h.dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		h.t.Fatal(err)
	}
	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		h.t.Fatal(err)
	}
	if err = h.db.Migrate(context.Background(), migrations); err != nil {
		h.t.Fatal(err)
	}
	h.deliveries = outbox.NewStore(h.db, outbox.Options{Now: h.clock.Now})
	if err = h.deliveries.Register("scheduled-effect", 1, func(p runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) {
		return runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -123, Kind: runtimecontract.ChatKindGroup}, Content: runtimecontract.MessageContent{Text: string(p.Data())}}, nil
	}); err != nil {
		h.t.Fatal(err)
	}
	h.deliveries.Seal()
	h.jobs = scheduler.New(h.db, h.clock)
	if err = h.jobs.Register("scheduled-feature", 1, h.handler); err != nil {
		h.t.Fatal(err)
	}
	h.jobs.SealRegistrations()
	if err = h.deliveries.Recover(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	if err = h.jobs.Recover(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	h.worker = h.newWorker("restarted")
}

func (h *scheduledRecovery) newWorker(owner string) *scheduler.Worker {
	h.t.Helper()
	w, err := scheduler.NewWorker(h.jobs, scheduler.WorkerOptions{Owner: owner, Lease: time.Minute, NewWorkScope: func(tx runtimecontract.DBTX) runtimecontract.WorkScope {
		return scheduledScope{tx, h.jobs, h.deliveries}
	}})
	if err != nil {
		h.t.Fatal(err)
	}
	return w
}

func scheduledRequest(t *testing.T, id runtimecontract.JobID, due time.Time) runtimecontract.JobRequest {
	t.Helper()
	p, err := runtimecontract.NewVersionedPayload(1, []byte("feature-payload"))
	if err != nil {
		t.Fatal(err)
	}

	return runtimecontract.JobRequest{ID: id, Type: "scheduled-feature", Payload: p, DueAt: due, Retry: runtimecontract.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Second, MaxDelay: 10 * time.Second}}
}

func (h *scheduledRecovery) schedule(id runtimecontract.JobID, due time.Time) {
	h.t.Helper()
	err := h.db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := h.jobs.Schedule(ctx, tx, scheduledRequest(h.t, id, due))
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *scheduledRecovery) status(id runtimecontract.JobID, state scheduler.State, attempts int) scheduler.JobStatus {
	h.t.Helper()
	s, err := h.jobs.Status(context.Background(), id)
	if err != nil || s.State != state || s.AttemptCount != attempts {
		h.t.Fatalf("%s status=%+v error=%v; want %s attempts=%d", id, s, err, state, attempts)
	}
	return s
}

func (h *scheduledRecovery) exec(query string) {
	h.t.Helper()
	if _, err := h.db.ExecContext(context.Background(), query); err != nil {
		h.t.Fatal(err)
	}
}

func (h *scheduledRecovery) run(want bool, wantErr error) {
	h.t.Helper()
	got, err := h.worker.RunOnce(context.Background())
	if got != want || !errors.Is(err, wantErr) {
		h.t.Fatalf("RunOnce=%v %v; want %v %v", got, err, want, wantErr)
	}
}

func (h *scheduledRecovery) counts(want int) {
	h.t.Helper()
	for _, query := range []string{`SELECT count(*) FROM scheduled_feature`, `SELECT count(*) FROM outbound_deliveries`, `SELECT count(*) FROM scheduled_jobs WHERE id LIKE 'child-%'`} {
		var n int

		if err := h.db.QueryRowContext(context.Background(), query).Scan(&n); err != nil || n != want {
			h.t.Fatalf("%s = %d %v; want %d", query, n, err, want)
		}
	}
}

func scheduledEffects(t *testing.T, ctx context.Context, scope runtimecontract.WorkScope, j scheduler.Job, now time.Time) {
	t.Helper()
	if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO scheduled_feature VALUES (?,?)`, j.Request.ID, "feature-value"); err != nil {
		t.Fatal(err)
	}

	if _, err := scope.EnqueueDelivery(ctx, runtimecontract.DeliveryRequest{Kind: "scheduled-effect", Payload: j.Request.Payload, Retry: j.Request.Retry}); err != nil {
		t.Fatal(err)
	}

	if _, err := scope.Schedule(ctx, scheduledRequest(t, "child-"+j.Request.ID, now.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
}

func TestScheduledRecoveryIntegrationReopenAndExactDueClaims(t *testing.T) {
	calls := make(map[runtimecontract.JobID]int)
	var h *scheduledRecovery

	h = newScheduledRecovery(t, func(ctx context.Context, scope runtimecontract.WorkScope, j scheduler.Job) scheduler.JobOutcome {
		calls[j.Request.ID]++
		scheduledEffects(t, ctx, scope, j, h.clock.Now())
		switch j.Request.ID {
		case "retry":
			if calls[j.Request.ID] == 1 {
				return scheduler.JobOutcome{Kind: scheduler.JobRetryable, RetryAfter: 2 * time.Minute, ReasonCode: "transient"}
			}
		case "permanent":
			return scheduler.JobOutcome{Kind: scheduler.JobPermanentFailure, ReasonCode: "permanent_failure"}
		}
		return scheduler.JobOutcome{Kind: scheduler.JobCompleted}
	})
	ctx := context.Background()
	for _, id := range []runtimecontract.JobID{"completed", "permanent", "retry"} {
		h.schedule(id, h.clock.Now())
		if id == "completed" {
			h.run(true, nil)
		} else {
			h.run(false, scheduler.ErrJobNotCompleted)
		}
	}

	h.schedule("expired", h.clock.Now())
	old, err := h.worker.ClaimDue(ctx)
	if err != nil || old == nil {
		t.Fatalf("old claim=%v %v", old, err)
	}

	h.schedule("overdue", h.clock.Now())
	h.schedule("cancelled", h.clock.Now())
	if err := h.jobs.Cancel(ctx, "cancelled"); err != nil {
		t.Fatal(err)
	}

	exact := h.clock.Now().Add(2 * time.Minute)
	h.schedule("exact", exact)
	h.schedule("cancelled-exact", exact)
	if err := h.jobs.Cancel(ctx, "cancelled-exact"); err != nil {
		t.Fatal(err)
	}

	h.clock.Advance(time.Minute)
	h.restart()
	h.status("completed", scheduler.StateCompleted, 1)
	h.status("permanent", scheduler.StatePermanentFailure, 1)
	h.status("cancelled", scheduler.StateCancelled, 0)
	h.status("expired", scheduler.StatePending, 1)
	retry := h.status("retry", scheduler.StateRetrying, 1)

	if retry.NextAttemptAt == nil || !retry.NextAttemptAt.Equal(exact) {
		t.Fatalf("retry schedule changed: %+v", retry)
	}

	if err := h.worker.ExecuteClaim(ctx, old); !errors.Is(err, scheduler.ErrClaimLost) {
		t.Fatalf("old claim after recovery=%v", err)
	}

	h.run(true, nil)
	h.run(true, nil)
	h.clock.Advance(time.Minute - time.Nanosecond)
	h.run(false, nil)
	// Independent workers contend for the two rows eligible at exactly this time.
	type result struct {
		worker *scheduler.Worker
		claim  *scheduler.Claim
		err    error
	}

	raceClaims := func() []result {
		results := make(chan result, 12)
		start := make(chan struct{})
		for i := range 12 {
			w := h.newWorker(string(rune('a' + i)))
			go func() { <-start; c, e := w.ClaimDue(ctx); results <- result{w, c, e} }()
		}

		close(start)
		winners := []result{}
		for range 12 {
			r := <-results
			if r.err != nil {
				t.Error(r.err)
			}

			if r.claim != nil {
				winners = append(winners, r)
			}
		}
		return winners
	}
	// Check concurrent eligibility on each side of the nanosecond boundary.
	if winners := raceClaims(); len(winners) != 0 {
		t.Fatalf("early concurrent claim winners=%d", len(winners))
	}

	h.clock.Advance(time.Nanosecond)
	winners := raceClaims()

	if len(winners) != 2 {
		t.Fatalf("exact-boundary winners=%d; want 2", len(winners))
	}

	for _, r := range winners {
		if err := r.worker.ExecuteClaim(ctx, r.claim); err != nil {
			t.Fatal(err)
		}

		if err := r.worker.ExecuteClaim(ctx, r.claim); !errors.Is(err, scheduler.ErrClaimLost) {
			t.Fatalf("repeated claim=%v", err)
		}
	}

	h.counts(5)
	h.restart()
	h.run(false, nil)
	for _, id := range []runtimecontract.JobID{"completed", "expired", "overdue", "retry", "exact"} {
		attempts := 1
		if id == "expired" || id == "retry" {
			attempts = 2
		}
		h.status(id, scheduler.StateCompleted, attempts)
	}

	if calls["cancelled-exact"] != 0 || calls["overdue"] != 1 || calls["cancelled"] != 0 || calls["expired"] != 1 || calls["retry"] != 2 || calls["exact"] != 1 || calls["completed"] != 1 || calls["permanent"] != 1 {
		t.Fatalf("unexpected executions: %v", calls)
	}

	h.counts(5)
}

func TestScheduledRecoveryIntegrationOutcomeAtomicity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		kind   scheduler.JobOutcomeKind
		state  scheduler.State
		reason string
		panic  bool
	}{
		{name: "completed", kind: scheduler.JobCompleted, state: scheduler.StateCompleted},
		{name: "retry", kind: scheduler.JobRetryable, state: scheduler.StateRetrying, reason: "unspecified_failure"},
		{name: "permanent", kind: scheduler.JobPermanentFailure, state: scheduler.StatePermanentFailure, reason: "permanent_failure"},
		{name: "error", kind: scheduler.JobFailed, state: scheduler.StatePermanentFailure, reason: "handler_error"},
		{name: "panic", state: scheduler.StatePermanentFailure, reason: "handler_panic", panic: true},
		{name: "cancelled", kind: scheduler.JobCancelled, state: scheduler.StateCancelled, reason: "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var h *scheduledRecovery

			h = newScheduledRecovery(t, func(ctx context.Context, scope runtimecontract.WorkScope, j scheduler.Job) scheduler.JobOutcome {
				scheduledEffects(t, ctx, scope, j, h.clock.Now())
				if tc.panic {
					panic("private-panic-marker")
				}
				reason := tc.reason
				if tc.kind == scheduler.JobFailed || tc.kind == scheduler.JobRetryable {
					reason = "private-error-marker"
				}
				return scheduler.JobOutcome{Kind: tc.kind, ReasonCode: reason}
			})
			h.schedule("parent", h.clock.Now())
			want := 0
			if tc.kind == scheduler.JobCompleted {
				h.run(true, nil)
				want = 1
			} else {
				h.run(false, scheduler.ErrJobNotCompleted)
			}
			s := h.status("parent", tc.state, 1)

			if s.ReasonCode != tc.reason {
				t.Fatalf("reason=%q; want %q", s.ReasonCode, tc.reason)
			}

			var cleared bool

			if err := h.db.QueryRowContext(context.Background(), `SELECT claim_owner IS NULL AND claim_correlation_id IS NULL AND lease_until IS NULL FROM scheduled_jobs WHERE id='parent'`).Scan(&cleared); err != nil || !cleared {
				t.Fatalf("outcome retained claim: cleared=%v error=%v", cleared, err)
			}

			h.counts(want)
			h.restart()
			h.status("parent", tc.state, 1)
			h.counts(want)
			h.run(false, nil)

			if tc.kind == scheduler.JobCompleted {
				var state, content string

				if err := h.db.QueryRowContext(context.Background(), `SELECT state,content FROM outbound_deliveries`).Scan(&state, &content); err != nil || state != "pending" || content != "feature-payload" {
					t.Fatalf("durable delivery=%q %q %v", state, content, err)
				}

				h.status("child-parent", scheduler.StatePending, 0)
			}
		})
	}
}

func TestScheduledRecoveryIntegrationCompletionFailureRollsBackAllStores(t *testing.T) {
	var h *scheduledRecovery

	h = newScheduledRecovery(t, func(ctx context.Context, scope runtimecontract.WorkScope, j scheduler.Job) scheduler.JobOutcome {
		scheduledEffects(t, ctx, scope, j, h.clock.Now())
		return scheduler.JobOutcome{Kind: scheduler.JobCompleted}
	})
	h.schedule("parent", h.clock.Now())
	// A late completion write fails after all real child effects were inserted.
	h.exec(`CREATE TRIGGER interrupt_scheduled_completion BEFORE UPDATE OF state ON scheduled_jobs WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'private-storage-marker'); END`)
	if ran, err := h.worker.RunOnce(context.Background()); ran || err == nil {
		t.Fatalf("completion failure=%v %v", ran, err)
	} else {
		var storage *scheduler.StoreError

		if !errors.As(err, &storage) || storage.Operation != "execute" {
			t.Fatalf("completion failure category=%v", err)
		}
	}
	h.status("parent", scheduler.StateClaimed, 1)
	h.counts(0)
	h.clock.Advance(time.Minute)
	h.restart()
	h.status("parent", scheduler.StatePending, 1)
	h.counts(0)
	h.exec(`DROP TRIGGER interrupt_scheduled_completion`)
	h.run(true, nil)
	h.counts(1)
	h.restart()
	h.run(false, nil)
	h.status("parent", scheduler.StateCompleted, 2)
	h.counts(1)
}

func TestScheduledRecoveryIntegrationContextCancellationRollsBackAllStores(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	calls := 0
	var h *scheduledRecovery

	h = newScheduledRecovery(t, func(ctx context.Context, scope runtimecontract.WorkScope, j scheduler.Job) scheduler.JobOutcome {
		calls++
		scheduledEffects(t, ctx, scope, j, h.clock.Now())
		if calls == 1 {
			cancel() // Interrupt after all three stores have tentative writes.
		}
		return scheduler.JobOutcome{Kind: scheduler.JobCompleted}
	})
	h.schedule("parent", h.clock.Now())
	if ran, err := h.worker.RunOnce(ctx); ran || !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted execution=%v %v", ran, err)
	}

	h.status("parent", scheduler.StateClaimed, 1)
	h.counts(0)
	h.clock.Advance(time.Minute)
	h.restart()
	h.status("parent", scheduler.StatePending, 1)
	h.counts(0)
	h.run(true, nil)
	h.restart()
	h.run(false, nil)
	h.status("parent", scheduler.StateCompleted, 2)
	h.counts(1)

	if calls != 2 {
		t.Fatalf("executions=%d; want one interrupted and one recovered", calls)
	}
}
