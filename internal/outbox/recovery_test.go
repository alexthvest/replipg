package outbox

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestRecoverAbandonedAttemptsAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openDB(t, dir)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	seedRecovery(t, db, now)
	attempts := recoverySnapshot(t, db, "SELECT * FROM outbound_attempts ORDER BY id")
	preserved := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries WHERE state!='attempting' ORDER BY id")
	facts := recoverySnapshot(t, db, "SELECT id,kind,content_version,content,destination_chat_id,attempt_count,created_at FROM outbound_deliveries ORDER BY id")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openDB(t, dir)
	store := NewStore(db, Options{Now: func() time.Time { return now }})
	if err := store.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	for _, id := range []runtimecontract.DeliveryID{"abandoned", "possibly-transmitted"} {
		status, err := store.Status(ctx, id)
		if err != nil {
			t.Fatal(err)
		}

		if status.State != StateReconciliationRequired || !status.NextAttemptAt.IsZero() || !status.TerminalAt.IsZero() || status.ReasonCode != "interrupted" || !status.UpdatedAt.Equal(now) || status.Attempts != 2 {
			t.Fatalf("recovered %s = %+v; unknown sends must require explicit reconciliation", id, status)
		}

		if strings.Contains(status.ReasonCode, "private") {
			t.Fatal("content exposed operationally")
		}
	}

	if got := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries WHERE id NOT IN ('abandoned','possibly-transmitted') ORDER BY id"); !reflect.DeepEqual(got, preserved) {
		t.Fatal("pending, retrying or definitive state changed")
	}

	if got := recoverySnapshot(t, db, "SELECT id,kind,content_version,content,destination_chat_id,attempt_count,created_at FROM outbound_deliveries ORDER BY id"); !reflect.DeepEqual(got, facts) {
		t.Fatal("original delivery facts changed")
	}

	if got := recoverySnapshot(t, db, "SELECT * FROM outbound_attempts ORDER BY id"); !reflect.DeepEqual(got, attempts) {
		t.Fatal("append-only original attempt facts changed")
	}

	snapshot := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id")
	store.now = func() time.Time { return now.Add(time.Hour) }
	if err := store.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if got := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id"); !reflect.DeepEqual(got, snapshot) {
		t.Fatal("repeated recovery must be idempotent")
	}

	store.now = func() time.Time { return now }
	store.Seal()
	worker, err := NewWorker(store, WorkerOptions{Delivery: recoveryNoSend{t}})
	if err != nil {
		t.Fatal(err)
	}

	claim, err := worker.ClaimDue(ctx)
	if err != nil || claim != nil {
		t.Fatalf("recovered unknown sends became eligible: claim=%v error=%v", claim, err)
	}
}

func TestRecoverRollsBackAllRecordsAndRedactsFailure(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, t.TempDir())
	now := time.Unix(10000, 0)
	seedRecovery(t, db, now)
	// FAIL preserves any earlier updates within a statement; the enclosing transaction
	// must roll them back when recovery cannot finish the entire batch.
	if _, err := db.ExecContext(ctx, `CREATE TRIGGER fail_recovery AFTER UPDATE ON outbound_deliveries WHEN NEW.id='possibly-transmitted' BEGIN SELECT RAISE(FAIL, 'private content in storage error'); END`); err != nil {
		t.Fatal(err)
	}

	before := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id")
	store := NewStore(db, Options{Now: func() time.Time { return now }})
	err := store.Recover(ctx)
	if !errors.Is(err, ErrStorage) || strings.Contains(err.Error(), "private") {
		t.Fatalf("recovery error must be safe: %v", err)
	}

	if got := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id"); !reflect.DeepEqual(got, before) {
		t.Fatal("failed recovery partially committed")
	}

	if _, err := db.ExecContext(ctx, "DROP TRIGGER fail_recovery"); err != nil {
		t.Fatal(err)
	}

	if err := store.Recover(ctx); err != nil {
		t.Fatalf("recovery after failure: %v", err)
	}
}

func TestRecoverCancellationAndClosedStorage(t *testing.T) {
	db := openDB(t, t.TempDir())
	now := time.Unix(10000, 0)
	seedRecovery(t, db, now)
	store := NewStore(db, Options{Now: func() time.Time { return now }})
	before := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Recover(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled recovery = %v", err)
	}

	if got := recoverySnapshot(t, db, "SELECT * FROM outbound_deliveries ORDER BY id"); !reflect.DeepEqual(got, before) {
		t.Fatal("cancelled recovery changed state")
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := store.Recover(context.Background()); !errors.Is(err, persistence.ErrPersistenceClosed) {
		t.Fatalf("closed recovery = %v", err)
	}
}

func seedRecovery(t *testing.T, db *persistence.DB, now time.Time) {
	t.Helper()
	ctx := context.Background()
	for _, row := range []struct {
		id    string
		state State
	}{
		{"abandoned", StateAttempting}, {"possibly-transmitted", StateAttempting},
		{"pending", StatePending}, {"retrying", StateRetrying}, {"reconciliation", StateReconciliationRequired},
		{"completed", StateCompleted}, {"failed", StatePermanentFailure},
	} {
		var next, terminal any
		var reason any

		if row.state == StatePending || row.state == StateRetrying {
			next = now.Add(time.Hour).UnixMilli()
		}
		if row.state == StateCompleted || row.state == StatePermanentFailure {
			terminal = now.Add(-time.Hour).UnixMilli()
		}
		if row.state == StatePermanentFailure || row.state == StateReconciliationRequired {
			reason = "indeterminate"
		}
		_, err := db.ExecContext(ctx, `INSERT INTO outbound_deliveries (id,kind,content_version,content,destination_chat_id,replace_callback_message,state,max_attempts,initial_delay_ms,max_delay_ms,attempt_count,next_attempt_at,reason_code,created_at,updated_at,terminal_at) VALUES (?, 'feature', 1, ?, -123, 0, ?, 3, 1000, 10000, 2, ?, ?, ?, ?, ?)`, row.id, []byte("private original content"), row.state, next, reason, now.Add(-2*time.Hour).UnixMilli(), now.Add(-time.Hour).UnixMilli(), terminal)
		if err != nil {
			t.Fatal(err)
		}
	}

	for _, id := range []string{"abandoned", "possibly-transmitted"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO outbound_attempts (id,delivery_id,correlation_id,started_at) VALUES (?, ?, ?, ?)`, id+"-active", id, "original-correlation", now.Add(-time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}

		if _, err := db.ExecContext(ctx, `INSERT INTO outbound_attempts (id,delivery_id,correlation_id,started_at,finished_at,outcome,reason_code) VALUES (?, ?, ?, ?, ?, 'retryable_failure','transient_error')`, id+"-previous", id, "previous-correlation", now.Add(-2*time.Hour).UnixMilli(), now.Add(-90*time.Minute).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
}

func recoverySnapshot(t *testing.T, db *persistence.DB, query string) [][]any {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}

	var snapshot [][]any

	for rows.Next() {
		values := make([]any, len(cols))
		dest := make([]any, len(cols))
		for i := range values {
			dest[i] = &values[i]
		}

		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}

		// Copy blobs because drivers may reuse backing storage across rows.
		for i, value := range values {
			if blob, ok := value.([]byte); ok {
				values[i] = append([]byte(nil), blob...)
			}
		}

		snapshot = append(snapshot, values)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	return snapshot
}

func TestRecoverRequiresTransactionCapability(t *testing.T) {
	db := openDB(t, t.TempDir())
	var typedNil *persistence.DB

	for _, capability := range []runtimecontract.DBTX{nil, typedNil, struct{ runtimecontract.DBTX }{db}} {
		store := NewStore(capability, Options{})
		if err := store.Recover(context.Background()); !errors.Is(err, ErrTransactionRequired) {
			t.Fatalf("nontransactional recovery = %v", err)
		}
	}
}

type recoveryNoSend struct{ t *testing.T }

func (port recoveryNoSend) Deliver(context.Context, runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
	port.t.Fatal("recovery must never send")
	return runtimecontract.DeliveryOutcome{}
}
