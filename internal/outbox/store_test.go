package outbox

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestEnqueueRecordsDelivery(t *testing.T) {
	dir := t.TempDir()
	db := openDB(t, dir)
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	store := newTestStore(t, db, now, publicResponse())
	req := deliveryRequest(t, 1)
	var id runtimecontract.DeliveryID
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error
		id, err = store.Enqueue(ctx, tx, req)
		return err
	})
	if err != nil {
		t.Fatalf("Enqueue() must persist accepted intent: %v", err)
	}
	if len(id) != 32 {
		t.Fatalf("delivery ID must be an opaque 128-bit identifier: %q", id)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openDB(t, dir)
	store = newTestStore(t, db, now, publicResponse())
	status, err := store.Status(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != StatePending || status.Attempts != 0 || status.MaxAttempts != 3 || !status.NextAttemptAt.Equal(now) || !status.CreatedAt.Equal(now) {
		t.Fatalf("persisted status = %+v", status)
	}
	var content []byte
	var version, initial, max, chat int64
	err = db.QueryRowContext(context.Background(), `SELECT content, content_version, initial_delay_ms, max_delay_ms, destination_chat_id FROM outbound_deliveries WHERE id = ?`, id).Scan(&content, &version, &initial, &max, &chat)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != string(req.Payload.Data()) || version != 1 || initial != 1000 || max != 10000 || chat != -123 {
		t.Fatal("request bytes, schema, retry policy or destination changed")
	}
}

func TestEnqueueRollbackAndConflict(t *testing.T) {
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "fixed-id", nil }
	abort := errors.New("abort feature operation")
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := store.Enqueue(ctx, tx, deliveryRequest(t, 1)); err != nil {
			return err
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("rollback = %v", err)
	}
	if _, err := store.Status(context.Background(), "fixed-id"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rolled-back status = %v", err)
	}
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
	err = enqueue(t, db, store, deliveryRequest(t, 1))
	var conflict *ConflictError
	if !errors.Is(err, ErrConflict) || !errors.As(err, &conflict) {
		t.Fatalf("collision = %v", err)
	}
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM outbound_deliveries`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("collision changed durable intent: %d, %v", count, err)
	}
}

func TestValidationAndVersionErrorsBeforeInsert(t *testing.T) {
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	for _, tc := range []struct {
		name   string
		change func(*runtimecontract.DeliveryRequest)
		target error
	}{
		{"unsupported version", func(r *runtimecontract.DeliveryRequest) { *r = deliveryRequest(t, 2) }, ErrUnsupportedVersion},
		{"unsupported kind", func(r *runtimecontract.DeliveryRequest) { r.Kind = "unknown" }, ErrUnsupportedVersion},
		{"invalid retry", func(r *runtimecontract.DeliveryRequest) { r.Retry.MaxAttempts = 0 }, nil},
		{"missing kind", func(r *runtimecontract.DeliveryRequest) { r.Kind = "" }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := deliveryRequest(t, 1)
			tc.change(&req)
			err := enqueue(t, db, store, req)
			if err == nil {
				t.Fatal("invalid request accepted")
			}
			if tc.target != nil && !errors.Is(err, tc.target) {
				t.Fatalf("error = %v", err)
			}
		})
	}
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM outbound_deliveries`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid intents persisted: %d, %v", count, err)
	}
}

func TestDecodedResponseValidation(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		response runtimecontract.ResponseRequest
	}{
		{"nil", nil},
		{"callback acknowledgement", runtimecontract.CallbackAcknowledgement{CallbackQueryID: "secret"}},
		{"invalid destination", runtimecontract.PublicResponse{Content: runtimecontract.MessageContent{Text: "private body"}}},
		{"expired authorization", runtimecontract.EphemeralResponse{Destination: runtimecontract.GroupDestination{ChatID: -123, Kind: runtimecontract.ChatKindGroup}, TargetUserID: 42, Content: runtimecontract.MessageContent{Text: "private body"}, Authorization: runtimecontract.CallbackAuthorization{CallbackQueryID: "secret", ValidUntil: now.Add(-time.Second)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t, t.TempDir())
			store := newTestStore(t, db, now, tc.response)
			if err := enqueue(t, db, store, deliveryRequest(t, 1)); err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestEphemeralRoutingMetadata(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		auth     runtimecontract.EphemeralAuthorization
		replace  bool
		wantKind string
	}{
		{"callback", runtimecontract.CallbackAuthorization{CallbackQueryID: "callback-secret", ValidUntil: now.Add(time.Minute)}, true, "callback"},
		{"incoming", runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: 7, ValidUntil: now.Add(time.Minute)}, false, "incoming_ephemeral"},
		{"administrator", runtimecontract.AdministratorAuthorization{}, false, "administrator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t, t.TempDir())
			response := runtimecontract.EphemeralResponse{Destination: runtimecontract.GroupDestination{ChatID: -123, Kind: runtimecontract.ChatKindSupergroup}, TargetUserID: 42, Content: runtimecontract.MessageContent{Text: "private body"}, Authorization: tc.auth, ReplaceCallbackMessage: tc.replace}
			store := newTestStore(t, db, now, &response)
			if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
				t.Fatal(err)
			}
			var kind string
			var target, replace int64
			if err := db.QueryRowContext(context.Background(), `SELECT authorization_kind, target_user_id, replace_callback_message FROM outbound_deliveries`).Scan(&kind, &target, &replace); err != nil {
				t.Fatal(err)
			}
			if kind != tc.wantKind || target != 42 || (replace == 1) != tc.replace {
				t.Fatal("ephemeral metadata changed")
			}
		})
	}
}

func TestSafeStatusForEveryState(t *testing.T) {
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC), publicResponse())
	store.generateID = func() (runtimecontract.DeliveryID, error) { return "fixed-id", nil }
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{StatePending, StateAttempting, StateRetrying, StateReconciliationRequired, StateCompleted, StatePermanentFailure} {
		var next, terminal any
		if state == StatePending || state == StateRetrying {
			next = int64(1000)
		}
		if state == StateCompleted || state == StatePermanentFailure {
			terminal = int64(2000)
		}
		_, err := db.ExecContext(context.Background(), `UPDATE outbound_deliveries SET state = ?, next_attempt_at = ?, terminal_at = ?, reason_code = ? WHERE id = ?`, state, next, terminal, "sensitive raw failure payload", "fixed-id")
		if err != nil {
			t.Fatal(err)
		}
		status, err := store.Status(context.Background(), "fixed-id")
		if err != nil || status.State != state {
			t.Fatalf("status = %+v, %v", status, err)
		}
		if strings.Contains(fmt.Sprintf("%+v", status), "sensitive") || strings.Contains(fmt.Sprintf("%+v", status), "private") {
			t.Fatal("status leaks content")
		}
	}
	if _, err := db.ExecContext(context.Background(), `UPDATE outbound_deliveries SET reason_code = 'retry_exhausted' WHERE id = 'fixed-id'`); err != nil {
		t.Fatal(err)
	}
	status, err := store.Status(context.Background(), "fixed-id")
	if err != nil || status.ReasonCode != "retry_exhausted" {
		t.Fatalf("safe failure reason = %+v, %v", status, err)
	}
	typ := reflect.TypeFor[DeliveryStatus]()
	for _, forbidden := range []string{"Content", "Payload", "Kind", "Destination", "Authorization"} {
		if _, ok := typ.FieldByName(forbidden); ok {
			t.Errorf("status exposes %s", forbidden)
		}
	}
}

func TestRegistrySealingAndDecoderFailures(t *testing.T) {
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	if err := store.Register("feature", 1, func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) { return nil, nil }); !errors.Is(err, ErrRegistrationConflict) {
		t.Fatalf("duplicate registration = %v", err)
	}
	if err := store.Register("bad", 1, nil); err == nil {
		t.Fatal("nil decoder accepted")
	}
	if err := store.Register("", 1, func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) { return nil, nil }); err == nil {
		t.Fatal("empty kind accepted")
	}
	if err := store.Register("broken", 1, func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) {
		return nil, errors.New("private response secret")
	}); err != nil {
		t.Fatal(err)
	}
	req := deliveryRequest(t, 1)
	req.Kind = "broken"
	err := enqueue(t, db, store, req)
	if !errors.Is(err, ErrDecode) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe decode failure = %v", err)
	}
	store.Seal()
	if err := store.Register("late", 1, func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) { return nil, nil }); !errors.Is(err, ErrRegistrySealed) {
		t.Fatalf("late registration = %v", err)
	}
	if err := enqueue(t, db, store, deliveryRequest(t, 1)); err != nil {
		t.Fatal(err)
	}
}

func TestRetryDurationsRoundUpWithoutEarlyRetry(t *testing.T) {
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	for _, delay := range []time.Duration{time.Nanosecond, 1900 * time.Microsecond} {
		req := deliveryRequest(t, 1)
		req.Retry.InitialDelay, req.Retry.MaxDelay = delay, delay
		if err := enqueue(t, db, store, req); err != nil {
			t.Fatalf("valid duration rejected: %v", err)
		}
	}
	var initial, max int64
	if err := db.QueryRowContext(context.Background(), `SELECT initial_delay_ms, max_delay_ms FROM outbound_deliveries ORDER BY initial_delay_ms DESC LIMIT 1`).Scan(&initial, &max); err != nil {
		t.Fatal(err)
	}
	if initial != 2 || max != 2 {
		t.Fatalf("retry rounded down: %d %d", initial, max)
	}
}

func TestNilTransactionAndSafeStorageErrors(t *testing.T) {
	db := openDB(t, t.TempDir())
	store := newTestStore(t, db, time.Unix(1000, 0), publicResponse())
	var typedNil *persistence.DB
	for _, tx := range []runtimecontract.DBTX{nil, typedNil} {
		if _, err := store.Enqueue(context.Background(), tx, deliveryRequest(t, 1)); err == nil {
			t.Fatal("nil transaction accepted")
		}
	}
	for _, sentinel := range []error{persistence.ErrContention, persistence.ErrPersistenceClosed, context.Canceled, context.DeadlineExceeded} {
		if err := storageError(context.Background(), fmt.Errorf("unsafe raw error: %w", sentinel)); !errors.Is(err, sentinel) || strings.Contains(err.Error(), "unsafe") {
			t.Fatalf("storage error = %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Status(context.Background(), "missing"); !errors.Is(err, persistence.ErrPersistenceClosed) {
		t.Fatalf("closed storage status = %v", err)
	}
}

func openDB(t *testing.T, dir string) *persistence.DB {
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

func newTestStore(t *testing.T, db runtimecontract.DBTX, now time.Time, response runtimecontract.ResponseRequest) *Store {
	t.Helper()
	store := NewStore(db, Options{Now: func() time.Time { return now }})
	if err := store.Register("feature", 1, func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) { return response, nil }); err != nil {
		t.Fatal(err)
	}
	return store
}

func deliveryRequest(t *testing.T, version uint16) runtimecontract.DeliveryRequest {
	t.Helper()
	payload, err := runtimecontract.NewVersionedPayload(version, []byte("opaque private payload"))
	if err != nil {
		t.Fatal(err)
	}
	return runtimecontract.DeliveryRequest{Kind: "feature", Payload: payload, Retry: runtimecontract.RetryPolicy{MaxAttempts: 3, InitialDelay: time.Second, MaxDelay: 10 * time.Second}}
}

func publicResponse() runtimecontract.PublicResponse {
	return runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -123, Kind: runtimecontract.ChatKindGroup}, Content: runtimecontract.MessageContent{Text: "private payload body"}}
}

func enqueue(t *testing.T, db *persistence.DB, store *Store, req runtimecontract.DeliveryRequest) error {
	t.Helper()
	return db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := store.Enqueue(ctx, tx, req)
		return err
	})
}
