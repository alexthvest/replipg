package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func TestCancelDurableAndIneligible(t *testing.T) {
	for _, state := range []State{StatePending, StateRetrying} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			db, s := openStore(t, dir)
			req := request(t)
			if err := schedule(ctx, db, s, req); err != nil {
				t.Fatal(err)
			}
			if state == StateRetrying {
				if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET state='retrying',attempt_count=1,next_attempt_at=?,reason_code='handler_error' WHERE id=?`, req.DueAt.UnixMilli(), req.ID); err != nil {
					t.Fatal(err)
				}
			}
			now := req.DueAt.Add(time.Hour + time.Nanosecond)
			s.clock = testsupport.NewManualClock(now)
			if err := s.Cancel(ctx, req.ID); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, s = openStore(t, dir)
			status, err := s.Status(ctx, req.ID)
			if err != nil {
				t.Fatal(err)
			}
			if status.State != StateCancelled || status.NextAttemptAt != nil || status.ReasonCode != "cancelled" {
				t.Fatalf("status = %+v", status)
			}
			var terminal, updated int64
			if err := db.QueryRowContext(ctx, `SELECT terminal_at,updated_at FROM scheduled_jobs WHERE id=?`, req.ID).Scan(&terminal, &updated); err != nil {
				t.Fatal(err)
			}
			if terminal != now.UnixMilli() || updated != terminal {
				t.Fatalf("timestamps = %d %d", terminal, updated)
			}
			if err := schedule(ctx, db, s, req); err != nil {
				t.Fatalf("idempotent schedule: %v", err)
			}
			var eligible int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_jobs WHERE (state='pending' AND due_at<=?) OR (state='retrying' AND next_attempt_at<=?)`, now.UnixMilli(), now.UnixMilli()).Scan(&eligible); err != nil {
				t.Fatal(err)
			}
			if eligible != 0 {
				t.Fatal("cancelled job remains due")
			}
			assertCancelConflict(t, s.Cancel(ctx, req.ID), StateCancelled)
		})
	}
}

func assertCancelConflict(t *testing.T, err error, state State) {
	t.Helper()
	var conflict *CancellationConflictError
	if !errors.Is(err, ErrCancellationConflict) || !errors.As(err, &conflict) || conflict.State != state {
		t.Fatalf("conflict = %v, want state %s", err, state)
	}
}

func TestCancelConflictsPreserveExistingState(t *testing.T) {
	for _, state := range []State{StateClaimed, StateCompleted, StatePermanentFailure} {
		t.Run(string(state), func(t *testing.T) {
			ctx := context.Background()
			db, s := openStore(t, t.TempDir())
			req := request(t)
			if err := schedule(ctx, db, s, req); err != nil {
				t.Fatal(err)
			}
			var owner, correlation, reason any
			var lease, terminal any
			if state == StateClaimed {
				owner, correlation, lease = "owner", "correlation", req.DueAt.UnixMilli()
			} else {
				terminal = req.DueAt.UnixMilli()
			}
			if state == StatePermanentFailure {
				reason = "permanent_failure"
			}
			if _, err := db.ExecContext(ctx, `UPDATE scheduled_jobs SET state=?,claim_owner=?,claim_correlation_id=?,lease_until=?,terminal_at=?,reason_code=? WHERE id=?`, state, owner, correlation, lease, terminal, reason, req.ID); err != nil {
				t.Fatal(err)
			}
			before, err := s.Status(ctx, req.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertCancelConflict(t, s.Cancel(ctx, req.ID), state)
			after, err := s.Status(ctx, req.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after != before {
				t.Fatalf("conflict changed job: %+v -> %+v", before, after)
			}
		})
	}
}

func TestConcurrentCancelOneTransition(t *testing.T) {
	ctx := context.Background()
	db, s := openStore(t, t.TempDir())
	req := request(t)
	if err := schedule(ctx, db, s, req); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	for range 8 {
		go func() { <-start; results <- s.Cancel(ctx, req.ID) }()
	}
	close(start)
	winners := 0
	for range 8 {
		if err := <-results; err == nil {
			winners++
		} else {
			assertCancelConflict(t, err, StateCancelled)
		}
	}
	if winners != 1 {
		t.Fatalf("transitions = %d", winners)
	}
}

// A DBTX wrapper intentionally hides the optional transaction capability.
type queryOnly struct{ runtimecontract.DBTX }

func TestCancelValidationAndCancellation(t *testing.T) {
	ctx := context.Background()
	db, s := openStore(t, t.TempDir())
	if err := s.Cancel(ctx, ""); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("empty ID = %v", err)
	}
	if err := s.Cancel(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing = %v", err)
	}
	for _, dbtx := range []runtimecontract.DBTX{nil, (*persistence.DB)(nil), queryOnly{db}} {
		if err := New(dbtx, s.clock).Cancel(ctx, "job"); !errors.Is(err, ErrTransactionRequired) {
			t.Fatalf("missing transactor = %v", err)
		}
	}
	req := request(t)
	if err := schedule(ctx, db, s, req); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := s.Cancel(cancelled, req.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("context = %v", err)
	}
	status, err := s.Status(ctx, req.ID)
	if err != nil || status.State != StatePending {
		t.Fatalf("cancelled context changed state: %+v %v", status, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(ctx, req.ID); !errors.Is(err, persistence.ErrPersistenceClosed) {
		t.Fatalf("closed = %v", err)
	}
}

func TestCancelStorageFailureRollsBackAndHidesPrivateDetails(t *testing.T) {
	ctx := context.Background()
	db, s := openStore(t, t.TempDir())
	req := request(t)
	if err := schedule(ctx, db, s, req); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER reject_cancel BEFORE UPDATE OF state ON scheduled_jobs WHEN NEW.state='cancelled' BEGIN SELECT RAISE(ABORT,'private-payload-database-details'); END`); err != nil {
		t.Fatal(err)
	}
	err := s.Cancel(ctx, req.ID)
	var storage *StoreError
	if !errors.As(err, &storage) || strings.Contains(fmt.Sprintf("%+v", err), "private") || errors.Unwrap(err) != nil {
		t.Fatalf("unsafe failure = %v", err)
	}
	status, err := s.Status(ctx, req.ID)
	if err != nil || status.State != StatePending {
		t.Fatalf("failed cancel changed state: %+v %v", status, err)
	}
}

type rollbackTransactor struct{ *persistence.DB }

func (db rollbackTransactor) WithinTransaction(ctx context.Context, fn func(context.Context, runtimecontract.DBTX) error) error {
	return db.DB.WithinTransaction(ctx, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := fn(ctx, tx); err != nil {
			return err
		}
		return errors.New("private-transaction-failure")
	})
}

func TestCancelDoesNotReportSuccessBeforeTransactionCommit(t *testing.T) {
	ctx := context.Background()
	db, s := openStore(t, t.TempDir())
	req := request(t)
	if err := schedule(ctx, db, s, req); err != nil {
		t.Fatal(err)
	}
	s = New(rollbackTransactor{db}, s.clock)
	err := s.Cancel(ctx, req.ID)
	var storage *StoreError
	if !errors.As(err, &storage) || errors.Unwrap(err) != nil {
		t.Fatalf("transaction failure = %v", err)
	}
	status, err := s.Status(ctx, req.ID)
	if err != nil || status.State != StatePending {
		t.Fatalf("rollback changed state: %+v %v", status, err)
	}
}
