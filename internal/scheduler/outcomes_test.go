package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func TestWorkerFailureOutcomesRollback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome JobOutcome
		panics  bool
		state   State
		reason  string
	}{
		{name: "retry", outcome: JobOutcome{Kind: "retryable", RetryAfter: 1500 * time.Millisecond, ReasonCode: "private payload"}, state: StateRetrying, reason: "unspecified_failure"},
		{name: "permanent", outcome: JobOutcome{Kind: "permanent_failure", ReasonCode: "permanent_failure"}, state: StatePermanentFailure, reason: "permanent_failure"},
		{name: "unsafe_reason", outcome: JobOutcome{Kind: "permanent_failure", ReasonCode: "private payload"}, state: StatePermanentFailure, reason: "unspecified_failure"},
		{name: "error", outcome: JobOutcome{Kind: "failed", ReasonCode: "private payload"}, state: StatePermanentFailure, reason: "handler_error"},
		{name: "invalid_outcome", outcome: JobOutcome{}, state: StatePermanentFailure, reason: "handler_error"},
		{name: "panic", panics: true, state: StatePermanentFailure, reason: "handler_panic"},
		{name: "cancelled_outcome", outcome: JobOutcome{Kind: JobCancelled}, state: StateCancelled, reason: "cancelled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
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

				if tc.panics {
					panic("private payload")
				}
				return tc.outcome
			}))
			clock.Advance(time.Nanosecond)
			ran, err := w.RunOnce(context.Background())
			if ran || !errors.Is(err, ErrJobNotCompleted) {
				t.Fatalf("run = %v %v", ran, err)
			}

			status, err := s.Status(context.Background(), r.ID)
			if err != nil || status.State != tc.state || status.ReasonCode != tc.reason || status.AttemptCount != 1 {
				t.Fatalf("status = %+v %v", status, err)
			}

			var stored string

			if err := db.QueryRowContext(context.Background(), `SELECT reason_code FROM scheduled_jobs WHERE id=?`, r.ID).Scan(&stored); err != nil || stored != tc.reason {
				t.Fatalf("stored reason = %q %v", stored, err)
			}

			for _, table := range []string{"feature_state", "feature_effects"} {
				var n int

				if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil || n != 0 {
					t.Fatalf("%s = %d %v", table, n, err)
				}
			}

			if _, err := s.Status(context.Background(), "child"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("child = %v", err)
			}

			if tc.state == StateRetrying {
				want := clock.Now().Add(tc.outcome.RetryAfter)

				if status.NextAttemptAt == nil || !status.NextAttemptAt.Equal(want) {
					t.Fatalf("retry = %+v want %s", status, want)
				}
			} else if claim, err := w.ClaimDue(context.Background()); err != nil || claim != nil {
				t.Fatalf("terminal claim = %v %v", claim, err)
			}
		})
	}
}

func TestWorkerRetryBackoffExhaustion(t *testing.T) {
	db, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(context.Context, runtimecontract.WorkScope, Job) JobOutcome { return JobOutcome{Kind: "retryable"} }))
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET max_attempts=5,initial_delay_ms=1000,max_delay_ms=3000 WHERE id=?`, r.ID); err != nil {
		t.Fatal(err)
	}

	clock.Advance(time.Nanosecond)
	for attempt, want := range []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 3 * time.Second, 0} {
		if _, err := w.RunOnce(ctx); !errors.Is(err, ErrJobNotCompleted) {
			t.Fatal(err)
		}

		status, err := s.Status(ctx, r.ID)
		if err != nil || status.AttemptCount != attempt+1 {
			t.Fatalf("status = %+v %v", status, err)
		}

		if want == 0 {
			if status.State != StatePermanentFailure || status.ReasonCode != "retry_exhausted" || status.NextAttemptAt != nil {
				t.Fatalf("exhausted = %+v", status)
			}

			break
		}

		if status.NextAttemptAt == nil || !status.NextAttemptAt.Equal(clock.Now().Add(want)) {
			t.Fatalf("retry = %+v want %s", status, want)
		}

		clock.Advance(want - time.Nanosecond)
		if claim, err := w.ClaimDue(ctx); err != nil || claim != nil {
			t.Fatalf("early retry = %v %v", claim, err)
		}

		clock.Advance(time.Nanosecond)
	}
}

func TestWorkerFailureTransitionRollbackAndFence(t *testing.T) {
	for _, mode := range []string{"transition_failure", "expired", "replaced"} {
		t.Run(mode, func(t *testing.T) {
			var clock *testsupport.ManualClock

			db, s, w, c, r := workerFixture(t, jobHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, _ Job) JobOutcome {
				if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO feature_state VALUES (1)`); err != nil {
					t.Fatal(err)
				}

				if mode == "expired" {
					clock.Advance(time.Minute)
				}
				return JobOutcome{Kind: "retryable"}
			}))
			clock = c
			clock.Advance(time.Nanosecond)
			ctx := context.Background()
			claim, err := w.ClaimDue(ctx)
			if err != nil {
				t.Fatal(err)
			}

			if mode == "transition_failure" {
				if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_retry BEFORE UPDATE OF state ON scheduled_jobs WHEN NEW.state='retrying' BEGIN SELECT RAISE(ABORT,'private payload'); END`); err != nil {
					t.Fatal(err)
				}
			}

			if mode == "replaced" {
				if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET claim_correlation_id='newer' WHERE id=?`, r.ID); err != nil {
					t.Fatal(err)
				}
			}

			err = w.ExecuteClaim(ctx, claim)
			if err == nil || strings.Contains(err.Error(), "private payload") {
				t.Fatalf("error = %v", err)
			}

			if mode != "transition_failure" && !errors.Is(err, ErrClaimLost) {
				t.Fatalf("lost claim = %v", err)
			}

			status, err := s.Status(ctx, r.ID)
			if err != nil || status.State != StateClaimed {
				t.Fatalf("status = %+v %v", status, err)
			}

			var n int

			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM feature_state`).Scan(&n); err != nil || n != 0 {
				t.Fatalf("partial writes = %d %v", n, err)
			}
		})
	}
}

func TestWorkerFailureCancellationKeepsClaim(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, _ Job) JobOutcome {
		if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO feature_state VALUES (1)`); err != nil {
			t.Fatal(err)
		}

		cancel()
		return JobOutcome{Kind: "retryable"}
	}))
	clock.Advance(time.Nanosecond)
	if _, err := w.RunOnce(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}

	status, err := s.Status(context.Background(), r.ID)
	if err != nil || status.State != StateClaimed {
		t.Fatalf("status = %+v %v", status, err)
	}

	var n int

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM feature_state`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial state = %d %v", n, err)
	}
}

func TestWorkerRetryBoundsAndRounding(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		attempts                int
		initial, maximum, after time.Duration
		want                    time.Duration
	}{
		{name: "cap_before_shift_overflow", attempts: 64, initial: time.Second, maximum: time.Minute, want: time.Minute},
		{name: "retry_after_exceeds_cap", maximum: time.Second, initial: time.Second, after: 2*time.Second + time.Nanosecond, want: 2*time.Second + time.Millisecond},
		{name: "duration_limit", attempts: 64, initial: time.Duration(1 << 62), maximum: time.Duration(1<<63 - 1), want: time.Duration(1<<63 - 1)},
		{name: "retry_after_limit", initial: time.Second, maximum: time.Second, after: time.Duration(1<<63 - 1), want: time.Duration(1<<63 - 1)},
		{name: "negative_after", initial: time.Second, maximum: time.Second, after: -time.Second, want: time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(_ context.Context, _ runtimecontract.WorkScope, j Job) JobOutcome {
				if j.Request.Retry.InitialDelay <= 0 || j.Request.Retry.MaxDelay <= 0 {
					t.Fatalf("policy overflow = %+v", j.Request.Retry)
				}

				return JobOutcome{Kind: JobRetryable, RetryAfter: tc.after}
			}))
			ctx := context.Background()
			if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET attempt_count=?,max_attempts=100,initial_delay_ms=?,max_delay_ms=? WHERE id=?`, max(0, tc.attempts-1), ceilDuration(tc.initial), ceilDuration(tc.maximum), r.ID); err != nil {
				t.Fatal(err)
			}

			clock.Advance(time.Nanosecond)
			if _, err := w.RunOnce(ctx); !errors.Is(err, ErrJobNotCompleted) {
				t.Fatal(err)
			}

			status, err := s.Status(ctx, r.ID)
			want := time.UnixMilli(ceilInstant(clock.Now().Add(tc.want)))
			if err != nil || status.State != StateRetrying || status.NextAttemptAt == nil || !status.NextAttemptAt.Equal(want) {
				t.Fatalf("retry = %+v %v want %s", status, err, want)
			}
		})
	}
}

// Replace ownership only after the feature transaction has rolled back and
// before the scheduler starts its outcome transaction.
type replaceAfterRollback struct {
	*persistence.DB
	replace func()
}

func (db *replaceAfterRollback) WithinTransaction(ctx context.Context, fn func(context.Context, runtimecontract.DBTX) error) error {
	err := db.DB.WithinTransaction(ctx, fn)
	if errors.Is(err, ErrJobNotCompleted) && db.replace != nil {
		replace := db.replace
		db.replace = nil
		replace()
	}
	return err
}

func TestWorkerFailureTransitionFencesReplacementAfterRollback(t *testing.T) {
	ctx := context.Background()
	db, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, _ Job) JobOutcome {
		if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO feature_state VALUES (1)`); err != nil {
			t.Fatal(err)
		}

		return JobOutcome{Kind: JobRetryable}
	}))
	s.db = &replaceAfterRollback{DB: db, replace: func() {
		var n int

		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM feature_state`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("replacement sees feature mutations: %d %v", n, err)
		}

		if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET claim_correlation_id='replacement',attempt_count=2 WHERE id=?`, r.ID); err != nil {
			t.Fatal(err)
		}
	}}
	clock.Advance(time.Nanosecond)
	if _, err := w.RunOnce(ctx); !errors.Is(err, ErrClaimLost) {
		t.Fatalf("stale transition = %v", err)
	}

	status, err := s.Status(ctx, r.ID)
	if err != nil || status.State != StateClaimed || status.AttemptCount != 2 {
		t.Fatalf("replacement = %+v %v", status, err)
	}

	var correlation string

	if err := db.QueryRowContext(ctx, `SELECT claim_correlation_id FROM scheduled_jobs WHERE id=?`, r.ID).Scan(&correlation); err != nil || correlation != "replacement" {
		t.Fatalf("correlation = %q %v", correlation, err)
	}
}

func TestWorkerFailureTransitionCommitFailure(t *testing.T) {
	db, s, w, clock, r := workerFixture(t, jobHandlerFunc(func(ctx context.Context, scope runtimecontract.WorkScope, _ Job) JobOutcome {
		if _, err := scope.DBTX().ExecContext(ctx, `INSERT INTO feature_state VALUES (1)`); err != nil {
			t.Fatal(err)
		}

		return JobOutcome{Kind: JobPermanentFailure, ReasonCode: "permanent_failure"}
	}))
	s.db = &selectedRollback{DB: db, failAt: 3}
	clock.Advance(time.Nanosecond)
	if _, err := w.RunOnce(context.Background()); err == nil || errors.Is(err, ErrJobNotCompleted) {
		t.Fatalf("uncommitted transition = %v", err)
	}

	status, err := s.Status(context.Background(), r.ID)
	if err != nil || status.State != StateClaimed || status.ReasonCode != "" {
		t.Fatalf("status = %+v %v", status, err)
	}

	var n int

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM feature_state`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("partial writes = %d %v", n, err)
	}
}
