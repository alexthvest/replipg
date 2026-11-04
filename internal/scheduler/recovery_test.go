package scheduler

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func recoveryWorker(t *testing.T, s *Service, calls *atomic.Int64) *Worker {
	t.Helper()
	s.handlers[registrationKey{"feature.work", 1}] = jobHandlerFunc(func(context.Context, runtimecontract.WorkScope, Job) JobOutcome {
		calls.Add(1)
		return JobOutcome{Kind: JobCompleted}
	})
	w, err := NewWorker(s, WorkerOptions{Owner: "restarted", Lease: time.Minute, NewWorkScope: func(tx runtimecontract.DBTX) runtimecontract.WorkScope { return workerScope{tx, s} }})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestRecoverReopenedStateEligibility(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		state                              State
		dueOffset, nextOffset, leaseOffset time.Duration
		attempts                           int
		want                               State
		eligible                           bool
	}{
		{name: "overdue", state: StatePending, dueOffset: -time.Hour, want: StatePending, eligible: true},
		{name: "pending_future", state: StatePending, dueOffset: time.Millisecond, want: StatePending},
		{name: "retry_due", state: StateRetrying, dueOffset: -time.Hour, attempts: 1, want: StateRetrying, eligible: true},
		{name: "retry_future", state: StateRetrying, dueOffset: -time.Hour, nextOffset: time.Millisecond, attempts: 1, want: StateRetrying},
		{name: "expired", state: StateClaimed, dueOffset: -time.Hour, leaseOffset: -time.Millisecond, attempts: 1, want: StatePending, eligible: true},
		{name: "expires_now", state: StateClaimed, dueOffset: -time.Hour, attempts: 1, want: StatePending, eligible: true},
		{name: "live_claim", state: StateClaimed, dueOffset: -time.Hour, leaseOffset: time.Millisecond, attempts: 1, want: StateClaimed},
		{name: "exhausted", state: StateClaimed, dueOffset: -time.Hour, attempts: 3, want: StatePermanentFailure},
		{name: "completed", state: StateCompleted, dueOffset: -time.Hour, attempts: 1, want: StateCompleted},
		{name: "cancelled", state: StateCancelled, dueOffset: -time.Hour, want: StateCancelled},
		{name: "permanent", state: StatePermanentFailure, dueOffset: -time.Hour, attempts: 1, want: StatePermanentFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			db, s := openStore(t, dir)
			r := request(t)
			now := r.DueAt
			clock := testsupport.NewManualClock(now)
			s.clock = clock
			r.DueAt = now.Add(tc.dueOffset)
			if err := schedule(ctx, db, s, r); err != nil {
				t.Fatal(err)
			}
			var next, owner, correlation, lease, terminal any
			if tc.state == StateRetrying {
				next = now.Add(tc.nextOffset).UnixMilli()
			}
			if tc.state == StateClaimed {
				owner, correlation, lease = "previous", "old-correlation", now.Add(tc.leaseOffset).UnixMilli()
			}
			if tc.state == StateCompleted || tc.state == StateCancelled || tc.state == StatePermanentFailure {
				terminal = now.Add(-time.Minute).UnixMilli()
			}
			if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET state=?,attempt_count=?,next_attempt_at=?,claim_owner=?,claim_correlation_id=?,lease_until=?,terminal_at=?,reason_code=CASE WHEN ?='permanent_failure' THEN 'permanent_failure' ELSE NULL END WHERE id=?`, tc.state, tc.attempts, next, owner, correlation, lease, terminal, tc.state, r.ID); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, s = openStore(t, dir)
			s.clock = clock
			for range 2 {
				if err := s.Recover(ctx); err != nil {
					t.Fatal(err)
				}
			}
			status, err := s.Status(ctx, r.ID)
			if err != nil || status.State != tc.want || status.AttemptCount != tc.attempts || !status.DueAt.Equal(r.DueAt) {
				t.Fatalf("recovered = %+v %v", status, err)
			}
			if tc.state == StateRetrying && (status.NextAttemptAt == nil || !status.NextAttemptAt.Equal(now.Add(tc.nextOffset))) {
				t.Fatalf("retry timing changed: %+v", status)
			}
			var gotOwner, gotCorrelation sql.NullString
			var gotLease, gotTerminal sql.NullInt64
			if err := db.QueryRowContext(ctx, `SELECT claim_owner,claim_correlation_id,lease_until,terminal_at FROM scheduled_jobs WHERE id=?`, r.ID).Scan(&gotOwner, &gotCorrelation, &gotLease, &gotTerminal); err != nil {
				t.Fatal(err)
			}
			if tc.want != StateClaimed && (gotOwner.Valid || gotCorrelation.Valid || gotLease.Valid) {
				t.Fatal("abandoned claim retained")
			}
			if tc.want == StateClaimed && (gotOwner.String != "previous" || gotCorrelation.String != "old-correlation" || gotLease.Int64 != lease) {
				t.Fatal("live lease changed")
			}
			if tc.name == "exhausted" && (status.ReasonCode != "retry_exhausted" || !gotTerminal.Valid || gotTerminal.Int64 != now.UnixMilli()) {
				t.Fatalf("exhaustion = %+v terminal=%v", status, gotTerminal)
			}
			if terminal != nil && (!gotTerminal.Valid || gotTerminal.Int64 != terminal) {
				t.Fatal("terminal fact changed")
			}
			var calls atomic.Int64
			w := recoveryWorker(t, s, &calls)
			ran, err := w.RunOnce(ctx)
			if err != nil || ran != tc.eligible {
				t.Fatalf("execution = %v %v", ran, err)
			}
			if ran, err := w.RunOnce(ctx); err != nil || ran {
				t.Fatalf("repeat = %v %v", ran, err)
			}
			if calls.Load() != int64(map[bool]int{true: 1, false: 0}[tc.eligible]) {
				t.Fatal("wrong handler count")
			}
		})
	}
}

func TestRecoverExpiredClaimBoundaryAndFence(t *testing.T) {
	var calls atomic.Int64
	_, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(context.Context, runtimecontract.WorkScope, Job) JobOutcome {
		calls.Add(1)
		return JobOutcome{Kind: JobCompleted}
	}))
	ctx := context.Background()
	clock.Advance(time.Nanosecond)
	old, err := w.ClaimDue(ctx)
	if err != nil || old == nil {
		t.Fatalf("claim = %v %v", old, err)
	}
	clock.Advance(time.Minute - time.Nanosecond)
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	status, _ := s.Status(ctx, r.ID)
	if status.State != StateClaimed {
		t.Fatal("lease stolen before boundary")
	}
	clock.Advance(time.Nanosecond)
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	replacement, err := w.ClaimDue(ctx)
	if err != nil || replacement == nil {
		t.Fatalf("replacement = %v %v", replacement, err)
	}
	if err := w.ExecuteClaim(ctx, old); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("old claim = %v", err)
	}
	if err := w.ExecuteClaim(ctx, replacement); err != nil {
		t.Fatal(err)
	}
	status, _ = s.Status(ctx, r.ID)
	if status.AttemptCount != 2 || calls.Load() != 1 {
		t.Fatalf("status=%+v calls=%d", status, calls.Load())
	}
}

func TestRecoverCancellationAndTransactionFailures(t *testing.T) {
	ctx := context.Background()
	db, s, w, clock, r := workerFixture(t, inertHandler{})
	clock.Advance(time.Nanosecond)
	if claim, err := w.ClaimDue(ctx); err != nil || claim == nil {
		t.Fatalf("claim = %v %v", claim, err)
	}
	clock.Advance(time.Minute)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Recover(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	status, _ := s.Status(ctx, r.ID)
	if status.State != StateClaimed {
		t.Fatal("cancelled recovery changed durable state")
	}
	s.db = &selectedRollback{DB: db, failAt: 1}
	if err := s.Recover(ctx); err == nil || err.Error() != "scheduler: recover failed" {
		t.Fatalf("rollback = %v", err)
	}
	status, _ = s.Status(ctx, r.ID)
	if status.State != StateClaimed {
		t.Fatal("failed recovery committed")
	}
	s.db = db
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if c, err := w.ClaimDue(ctx); err != nil || c != nil {
		t.Fatalf("cancelled claim = %v %v", c, err)
	}
	s.db = &queryOnly{db}
	if err := s.Recover(ctx); !errors.Is(err, ErrTransactionRequired) {
		t.Fatalf("transaction required = %v", err)
	}
}

func TestRecoverConcurrentClaims(t *testing.T) {
	var calls atomic.Int64
	_, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(context.Context, runtimecontract.WorkScope, Job) JobOutcome {
		calls.Add(1)
		return JobOutcome{Kind: JobCompleted}
	}))
	ctx := context.Background()
	clock.Advance(time.Nanosecond)
	if c, err := w.ClaimDue(ctx); err != nil || c == nil {
		t.Fatalf("first claim = %v %v", c, err)
	}
	clock.Advance(time.Minute)
	type result struct {
		claim *Claim
		err   error
	}
	results := make(chan result, 12)
	start := make(chan struct{})
	for i := range 12 {
		go func() {
			<-start
			if i%2 == 0 {
				results <- result{err: s.Recover(ctx)}
				return
			}
			c, err := w.ClaimDue(ctx)
			results <- result{claim: c, err: err}
		}()
	}
	close(start)
	var winner *Claim
	for range 12 {
		got := <-results
		if got.err != nil {
			t.Error(got.err)
		}
		if got.claim != nil {
			if winner != nil {
				t.Error("multiple recovery claim winners")
			}
			winner = got.claim
		}
	}
	// Claimers may all precede recovery; the final claim must still be unique.
	c, err := w.ClaimDue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c != nil {
		if winner != nil {
			t.Fatal("recovery stole a fresh claim")
		}
		winner = c
	}
	if winner == nil {
		t.Fatal("recovered job ineligible")
	}
	if err := w.ExecuteClaim(ctx, winner); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status(ctx, r.ID)
	if err != nil || status.State != StateCompleted || status.AttemptCount != 2 || calls.Load() != 1 {
		t.Fatalf("status=%+v error=%v calls=%d", status, err, calls.Load())
	}
}
