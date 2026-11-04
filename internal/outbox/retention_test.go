package outbox

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestPurgeResolvedEligibility(t *testing.T) {
	cutoff := time.Unix(2000, 0).UTC()
	for _, tc := range []struct {
		name              string
		state             State
		terminal, updated time.Time
		want              bool
	}{
		{"completed inclusive", StateCompleted, cutoff, cutoff.Add(time.Hour), true},
		{"failed inclusive", StatePermanentFailure, cutoff, cutoff.Add(time.Hour), true},
		{"completed old", StateCompleted, cutoff.Add(-time.Millisecond), cutoff, true},
		{"completed young", StateCompleted, cutoff.Add(time.Millisecond), cutoff.Add(-time.Hour), false},
		{"failed young", StatePermanentFailure, cutoff.Add(time.Millisecond), cutoff.Add(-time.Hour), false},
		{"quarantined inclusive", StateReconciliationRequired, time.Time{}, cutoff, true},
		{"quarantined old", StateReconciliationRequired, time.Time{}, cutoff.Add(-time.Millisecond), true},
		{"quarantined young", StateReconciliationRequired, time.Time{}, cutoff.Add(time.Millisecond), false},
		{"pending", StatePending, time.Time{}, cutoff.Add(-time.Hour), false},
		{"attempting", StateAttempting, time.Time{}, cutoff.Add(-time.Hour), false},
		{"retrying", StateRetrying, time.Time{}, cutoff.Add(-time.Hour), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, store := retentionFixture(t, tc.state, tc.terminal, tc.updated)
			err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				got, err := store.PurgeResolved(ctx, tx, "feature", "retained", cutoff)
				if err != nil {
					return err
				}
				if got != tc.want {
					t.Errorf("purged = %v, want %v", got, tc.want)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if tc.want {
				wantCount = 0
			}
			assertRetentionCounts(t, db, wantCount)
			if tc.want {
				err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
					purged, err := store.PurgeResolved(ctx, tx, "feature", "retained", cutoff)
					if !purged || err != nil {
						t.Errorf("repeat purge = %v, %v", purged, err)
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestPurgeResolvedKindValidation(t *testing.T) {
	cutoff := time.Unix(2000, 0).UTC()
	db, store := retentionFixture(t, StateCompleted, cutoff, cutoff)
	if err := store.Register("other", 1, func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) {
		return publicResponse(), nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		kind   runtimecontract.DeliveryKind
		id     runtimecontract.DeliveryID
		target error
	}{
		{"unregistered", "retained", ErrUnsupportedVersion},
		{"unregistered", "missing", ErrUnsupportedVersion},
		{"other", "retained", ErrConflict},
	} {
		err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
			purged, err := store.PurgeResolved(ctx, tx, tc.kind, tc.id, cutoff)
			if purged || !errors.Is(err, tc.target) {
				t.Errorf("purge kind %q = %v, %v", tc.kind, purged, err)
			}
			if tc.target == ErrConflict {
				var conflict *ConflictError
				if !errors.As(err, &conflict) {
					t.Errorf("conflict is not typed: %T", err)
				}
			}
			if tc.target == ErrUnsupportedVersion {
				var unsupported *UnsupportedVersionError
				if !errors.As(err, &unsupported) {
					t.Errorf("unsupported is not typed: %T", err)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	assertRetentionCounts(t, db, 1)
}

func TestPurgeResolvedRollback(t *testing.T) {
	cutoff := time.Unix(2000, 0).UTC()
	db, store := retentionFixture(t, StateCompleted, cutoff, cutoff)
	abort := errors.New("abort feature cleanup")
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		purged, err := store.PurgeResolved(ctx, tx, "feature", "retained", cutoff)
		if err != nil {
			return err
		}
		if !purged {
			t.Error("eligible row not purged")
		}
		if _, err := store.StatusInTransaction(ctx, tx, "retained"); !errors.Is(err, ErrNotFound) {
			t.Errorf("status after transactional purge = %v", err)
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("rollback = %v", err)
	}
	assertRetentionCounts(t, db, 1)
}

func TestPurgeResolvedPreservesUnrelatedDelivery(t *testing.T) {
	cutoff := time.Unix(2000, 0).UTC()
	db, store := retentionFixture(t, StateCompleted, cutoff, cutoff)
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "unrelated", nil }
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `INSERT INTO outbound_attempts(id,delivery_id,correlation_id,started_at) VALUES('unrelated-attempt','unrelated','safe-correlation',1000000)`); err != nil {
		t.Fatal(err)
	}
	if err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		purged, err := store.PurgeResolved(ctx, tx, "feature", "retained", cutoff)
		if !purged {
			t.Error("eligible delivery not purged")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertRetentionCounts(t, db, 1)
	status, err := store.Status(context.Background(), "unrelated")
	if err != nil || status.State != StatePending {
		t.Fatalf("unrelated delivery = %+v, %v", status, err)
	}
	var payload []byte
	if err := db.QueryRowContext(context.Background(), `SELECT content FROM outbound_deliveries WHERE id='unrelated'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if string(payload) != string(deliveryRequest(t, 1).Payload.Data()) {
		t.Fatal("unrelated payload changed")
	}
}

func TestPurgeResolvedParentFailureRollsBackAttempts(t *testing.T) {
	cutoff := time.Unix(2000, 0).UTC()
	db, store := retentionFixture(t, StateCompleted, cutoff, cutoff)
	if _, err := db.ExecContext(context.Background(), `CREATE TRIGGER prevent_delivery_delete BEFORE DELETE ON outbound_deliveries BEGIN SELECT RAISE(ABORT,'private failure payload'); END`); err != nil {
		t.Fatal(err)
	}
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := store.PurgeResolved(ctx, tx, "feature", "retained", cutoff)
		return err
	})
	if !errors.Is(err, ErrStorage) || strings.Contains(err.Error(), "private") {
		t.Fatalf("delete failure = %v", err)
	}
	assertRetentionCounts(t, db, 1)
}

func TestPurgeResolvedFencesStateChangeBeforeDeletion(t *testing.T) {
	cutoff := time.Unix(2000, 0).UTC()
	db, store := retentionFixture(t, StateReconciliationRequired, time.Time{}, cutoff)
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		fenced := &retentionStateChange{DBTX: tx}
		purged, err := store.PurgeResolved(ctx, fenced, "feature", "retained", cutoff)
		if purged {
			t.Error("purged delivery after it became pending")
		}
		if !fenced.changed {
			t.Error("deletion boundary was not exercised")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	assertRetentionCounts(t, db, 1)
}

type retentionStateChange struct {
	runtimecontract.DBTX
	changed bool
}

func (tx *retentionStateChange) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if !tx.changed && strings.Contains(query, "DELETE FROM outbound_attempts") {
		tx.changed = true
		if _, err := tx.DBTX.ExecContext(ctx, `UPDATE outbound_deliveries SET state='pending',next_attempt_at=2000000,reason_code=NULL WHERE id='retained'`); err != nil {
			return nil, err
		}
	}
	return tx.DBTX.ExecContext(ctx, query, args...)
}

func TestPurgeResolvedValidatesInputs(t *testing.T) {
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	var typedNil *persistence.DB
	for _, tc := range []struct {
		tx   runtimecontract.DBTX
		kind runtimecontract.DeliveryKind
		id   runtimecontract.DeliveryID
	}{
		{nil, "feature", "id"}, {typedNil, "feature", "id"}, {db, "", "id"}, {db, "feature", ""},
	} {
		purged, err := store.PurgeResolved(context.Background(), tc.tx, tc.kind, tc.id, time.Unix(2000, 0))
		var invalid *runtimecontract.ValidationError
		if purged || !errors.As(err, &invalid) {
			t.Errorf("invalid purge = %v, %v", purged, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.PurgeResolved(ctx, nil, "feature", "id", time.Unix(2000, 0)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled purge = %v", err)
	}
}

func retentionFixture(t *testing.T, state State, terminal, updated time.Time) (*persistence.DB, *Store) {
	t.Helper()
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "retained", nil }
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
	var next, terminalMS any
	if state == StatePending || state == StateRetrying {
		next = int64(1000000)
	}
	if !terminal.IsZero() {
		terminalMS = terminal.UnixMilli()
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE outbound_deliveries SET state=?,next_attempt_at=?,terminal_at=?,updated_at=?,reason_code='indeterminate' WHERE id='retained'`, state, next, terminalMS, updated.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), `INSERT INTO outbound_attempts(id,delivery_id,correlation_id,started_at) VALUES('attempt','retained','safe-correlation',1000000)`); err != nil {
		t.Fatal(err)
	}
	return db, store
}

func assertRetentionCounts(t *testing.T, tx runtimecontract.DBTX, want int) {
	t.Helper()
	for _, table := range []string{"outbound_deliveries", "outbound_attempts"} {
		var count int
		if err := tx.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s count = %d, want %d", table, count, want)
		}
	}
}
