package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

type jobHandlerFunc func(context.Context, runtimecontract.WorkScope, Job) JobOutcome

func (f jobHandlerFunc) Execute(c context.Context, s runtimecontract.WorkScope, j Job) JobOutcome {
	return f(c, s, j)
}

type workerScope struct {
	tx        runtimecontract.DBTX
	scheduler *Service
}

func (s workerScope) DBTX() runtimecontract.DBTX { return s.tx }
func (s workerScope) Schedule(ctx context.Context, r runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	return s.scheduler.Schedule(ctx, s.tx, r)
}
func (s workerScope) EnqueueDelivery(ctx context.Context, r runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	_, err := s.tx.ExecContext(ctx, `INSERT INTO feature_effects VALUES (?)`, r.Kind)
	return "delivery", err
}
func workerFixture(t *testing.T, handler JobHandler) (*persistence.DB, *Service, *Worker, *testsupport.ManualClock, runtimecontract.JobRequest) {
	t.Helper()
	db, _ := openStore(t, t.TempDir())
	r := request(t)
	clock := testsupport.NewManualClock(r.DueAt.Add(-time.Nanosecond))
	s := New(db, clock)
	if err := s.Register(r.Type, 1, handler); err != nil {
		t.Fatal(err)
	}
	s.SealRegistrations()
	if _, err := db.ExecContext(context.Background(), `CREATE TABLE feature_state (value INTEGER); CREATE TABLE feature_effects (kind TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := schedule(context.Background(), db, s, r); err != nil {
		t.Fatal(err)
	}
	var ids atomic.Int64
	w, err := NewWorker(s, WorkerOptions{Owner: "worker", Lease: time.Minute, NewCorrelationID: func() (runtimecontract.CorrelationID, error) {
		return runtimecontract.CorrelationID(time.Unix(ids.Add(1), 0).String()), nil
	}, NewWorkScope: func(tx runtimecontract.DBTX) runtimecontract.WorkScope { return workerScope{tx, s} }})
	if err != nil {
		t.Fatal(err)
	}
	return db, s, w, clock, r
}
func TestWorkerDueCompletionAndEffects(t *testing.T) {
	var calls int
	db, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, j Job) JobOutcome {
		calls++
		if j.Request.ID != "stable-job" {
			t.Errorf("job identity = %s", j.Request.ID)
		}
		if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO feature_state VALUES (1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := scope.EnqueueDelivery(ctx, runtimecontract.DeliveryRequest{Kind: "effect"}); err != nil {
			t.Fatal(err)
		}
		child := request(t)
		child.ID = "child"
		child.DueAt = child.DueAt.Add(time.Hour)
		if _, err := scope.Schedule(ctx, child); err != nil {
			t.Fatal(err)
		}
		return JobOutcome{Kind: JobCompleted}
	}))
	ctx := context.Background()
	if ran, err := w.RunOnce(ctx); err != nil || ran {
		t.Fatalf("early = %v %v", ran, err)
	}
	clock.Advance(time.Nanosecond)
	if ran, err := w.RunOnce(ctx); err != nil || !ran {
		t.Fatalf("due = %v %v", ran, err)
	}
	if ran, err := w.RunOnce(ctx); err != nil || ran {
		t.Fatalf("repeat = %v %v", ran, err)
	}
	status, err := s.Status(ctx, r.ID)
	if err != nil || status.State != StateCompleted || status.AttemptCount != 1 || calls != 1 {
		t.Fatalf("completion = %+v %v calls=%d", status, err, calls)
	}
	for _, table := range []string{"feature_state", "feature_effects"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s = %d %v", table, n, err)
		}
	}
	child, err := s.Status(ctx, "child")
	if err != nil || child.State != StatePending {
		t.Fatalf("child = %+v %v", child, err)
	}
}
func TestWorkerCompetingClaimsAndFence(t *testing.T) {
	var calls atomic.Int64
	db, _, w, clock, r := workerFixture(t, jobHandlerFunc(func(context.Context, runtimecontract.WorkScope, Job) JobOutcome {
		calls.Add(1)
		return JobOutcome{Kind: JobCompleted}
	}))
	clock.Advance(time.Nanosecond)
	ctx := context.Background()
	results := make(chan *Claim, 8)
	errs := make(chan error, 8)
	start := make(chan struct{})
	for range 8 {
		go func() { <-start; c, e := w.ClaimDue(ctx); results <- c; errs <- e }()
	}
	close(start)
	var claim *Claim
	for range 8 {
		c := <-results
		if c != nil {
			if claim != nil {
				t.Fatal("multiple claim winners")
			}
			claim = c
		}
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if claim == nil {
		t.Fatal("no due claim")
	}
	var owner, correlation string
	var lease int64
	if err := db.QueryRowContext(ctx, `SELECT claim_owner,claim_correlation_id,lease_until FROM scheduled_jobs WHERE id=?`, r.ID).Scan(&owner, &correlation, &lease); err != nil || owner == "" || correlation == "" || lease != clock.Now().Add(time.Minute).UnixMilli() {
		t.Fatalf("lease = %s %s %d %v", owner, correlation, lease, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET claim_correlation_id='replacement' WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.ExecuteClaim(ctx, claim); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale = %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("stale handler executed")
	}
}
func TestWorkerCompletionFailureRollsBackEffects(t *testing.T) {
	db, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, _ Job) JobOutcome {
		if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO feature_state VALUES (1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := scope.EnqueueDelivery(ctx, runtimecontract.DeliveryRequest{Kind: "effect"}); err != nil {
			t.Fatal(err)
		}
		child := request(t)
		child.ID = "child"
		if _, err := scope.Schedule(ctx, child); err != nil {
			t.Fatal(err)
		}
		return JobOutcome{Kind: JobCompleted}
	}))
	clock.Advance(time.Nanosecond)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_completion BEFORE UPDATE OF state ON scheduled_jobs WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'private-data'); END`); err != nil {
		t.Fatal(err)
	}
	if ran, err := w.RunOnce(ctx); err == nil || ran {
		t.Fatalf("failed completion = %v %v", ran, err)
	}
	status, err := s.Status(ctx, r.ID)
	if err != nil || status.State != StateClaimed {
		t.Fatalf("status = %+v %v", status, err)
	}
	for _, table := range []string{"feature_state", "feature_effects"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s = %d %v", table, n, err)
		}
	}
	if _, err := s.Status(ctx, "child"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("child persisted: %v", err)
	}
}

func TestWorkerExpiredLeaseNeverCommits(t *testing.T) {
	for _, duringHandler := range []bool{false, true} {
		t.Run(map[bool]string{false: "before_handler", true: "during_handler"}[duringHandler], func(t *testing.T) {
			var clock *testsupport.ManualClock
			calls := 0
			db, s, w, c, r := workerFixture(t, jobHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, _ Job) JobOutcome {
				calls++
				if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO feature_state VALUES (1)`); err != nil {
					t.Fatal(err)
				}
				clock.Advance(time.Minute)
				return JobOutcome{Kind: JobCompleted}
			}))
			clock = c
			clock.Advance(time.Nanosecond)
			ctx := context.Background()
			claim, err := w.ClaimDue(ctx)
			if err != nil || claim == nil {
				t.Fatalf("claim = %v %v", claim, err)
			}
			if !duringHandler {
				clock.Advance(time.Minute)
			}
			if err := w.ExecuteClaim(ctx, claim); !errors.Is(err, ErrClaimLost) {
				t.Fatalf("expired = %v", err)
			}
			if duringHandler && calls != 1 || !duringHandler && calls != 0 {
				t.Fatalf("calls = %d", calls)
			}
			var n int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM feature_state`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("partial state = %d %v", n, err)
			}
			status, err := s.Status(ctx, r.ID)
			if err != nil || status.State != StateClaimed {
				t.Fatalf("status = %+v %v", status, err)
			}
		})
	}
}

func TestWorkerRetryEligibilityAndCompletedDurability(t *testing.T) {
	db, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(context.Context, runtimecontract.WorkScope, Job) JobOutcome {
		return JobOutcome{Kind: JobCompleted}
	}))
	ctx := context.Background()
	clock.Advance(time.Nanosecond)
	next := clock.Now().Add(time.Second)
	if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET state='retrying',attempt_count=1,next_attempt_at=? WHERE id=?`, next.UnixMilli(), r.ID); err != nil {
		t.Fatal(err)
	}
	if claim, err := w.ClaimDue(ctx); err != nil || claim != nil {
		t.Fatalf("early retry = %v %v", claim, err)
	}
	clock.Advance(time.Second)
	if ran, err := w.RunOnce(ctx); err != nil || !ran {
		t.Fatalf("due retry = %v %v", ran, err)
	}
	status, err := s.Status(ctx, r.ID)
	if err != nil || status.AttemptCount != 2 || status.State != StateCompleted {
		t.Fatalf("completed = %+v %v", status, err)
	}
	// Reopening the same persisted store must preserve terminal ineligibility.
	var dir string
	if err := db.QueryRowContext(ctx, `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&dir); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, s2 := openStore(t, filepath.Dir(dir))
	s2.clock = clock
	w2, err := NewWorker(s2, w.options)
	if err != nil {
		t.Fatal(err)
	}
	if claim, err := w2.ClaimDue(ctx); err != nil || claim != nil {
		t.Fatalf("reopened completed = %v %v", claim, err)
	}
	_ = reopened
}

// Fail a selected transaction after its callback, exercising commit-boundary
// behavior without relying on SQLite timing or disk failure.
type selectedRollback struct {
	*persistence.DB
	calls  int
	failAt int
}

func (db *selectedRollback) WithinTransaction(ctx context.Context, fn func(context.Context, runtimecontract.DBTX) error) error {
	db.calls++
	return db.DB.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		if db.calls == db.failAt {
			return errors.New("private transaction failure")
		}
		return nil
	})
}
func TestWorkerDoesNotReportUncommittedWork(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(map[int]string{1: "claim", 2: "completion"}[failAt], func(t *testing.T) {
			db, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, _ Job) JobOutcome {
				if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO feature_state VALUES (1)`); err != nil {
					t.Fatal(err)
				}
				return JobOutcome{Kind: JobCompleted}
			}))
			s.db = &selectedRollback{DB: db, failAt: failAt}
			clock.Advance(time.Nanosecond)
			if ran, err := w.RunOnce(context.Background()); ran || err == nil {
				t.Fatalf("uncommitted work = %v %v", ran, err)
			}
			status, err := s.Status(context.Background(), r.ID)
			want := StatePending
			if failAt == 2 {
				want = StateClaimed
			}
			if err != nil || status.State != want {
				t.Fatalf("status = %+v %v", status, err)
			}
			var n int
			if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM feature_state`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("partial state = %d %v", n, err)
			}
		})
	}
}
