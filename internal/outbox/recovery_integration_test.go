package outbox_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/telegram"
	"github.com/alexthvest/replipg/internal/testsupport"
)

const recoveryBody = "private-response-marker"

// These tests cross the real SQLite/worker/Telegram adapter boundaries. Only the
// remote Bot API and time are fake; no delivery outcome is supplied by a mock.
func TestDeliveryRecoveryIntegrationEnqueueAndRetryWait(t *testing.T) {
	h := newDeliveryRecovery(t)
	id := h.enqueue(3)
	h.restart(nil) // Interruption after enqueue, before any transport attempt.
	h.status(id, outbox.StatePending, 0)
	h.requests(0)
	h.fake.Enqueue(apiResponse(`{"ok":false,"error_code":429,"description":"private-api-marker","parameters":{"retry_after":17}}`))
	h.run(false, outbox.ErrDeliveryNotCompleted)
	status := h.status(id, outbox.StateRetrying, 1)

	if !status.NextAttemptAt.Equal(h.clock.Now().Add(17 * time.Second)) {
		t.Fatalf("Telegram retry_after floor lost: %+v", status)
	}

	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan error, 1)
	go func() { waited <- h.worker.WaitNext(ctx, time.Minute) }()
	gate, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()

	if err := h.clock.WaitForTimers(gate, 1); err != nil {
		cancel()
		t.Fatal(err)
	}

	cancel() // Shutdown while sleeping must not need a clock advance.
	select {
	case err := <-waited:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("retry wait cancellation = %v", err)
		}
	case <-gate.Done():
		t.Fatal("retry wait did not stop on cancellation")
	}

	if h.clock.PendingTimers() != 0 {
		t.Fatal("cancelled retry wait leaked a timer")
	}

	h.restart(nil)
	h.status(id, outbox.StateRetrying, 1)
	h.clock.Advance(17*time.Second - time.Millisecond)
	h.run(false, nil)
	h.requests(1)
	h.clock.Advance(time.Millisecond)
	h.fake.Enqueue(acceptedResponse())
	h.run(true, nil)
	h.status(id, outbox.StateCompleted, 2)
	h.restart(nil)
	h.clock.Advance(time.Hour)
	h.run(false, nil)
	h.requests(2)
}

func TestDeliveryRecoveryIntegrationUnknownTransmission(t *testing.T) {
	for _, interruption := range []string{"during_transmission", "connection_drop", "accepted_before_completion_commit"} {
		for _, resolution := range []outbox.ReconciliationDecision{outbox.ReconciliationConfirm, outbox.ReconciliationRetry} {
			t.Run(interruption+"/"+string(resolution), func(t *testing.T) {
				h := newDeliveryRecovery(t)
				id := h.enqueue(3)
				switch interruption {
				case "during_transmission":
					h.fake.Enqueue(testsupport.TelegramResponse{WaitForCancellation: true})
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()

					done := make(chan error, 1)
					go func() { _, err := h.worker.RunOnce(ctx); done <- err }()
					gate, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()

					if err := h.fake.WaitForRequests(gate, 2); err != nil {
						t.Fatal(err)
					}

					h.status(id, outbox.StateAttempting, 1)
					cancel()
					select {
					case err := <-done:
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("interrupted send = %v", err)
						}
					case <-gate.Done():
						t.Fatal("transmission did not stop on cancellation")
					}

					h.status(id, outbox.StateAttempting, 1)
				case "connection_drop":
					h.fake.Enqueue(testsupport.TelegramResponse{DropConnection: true})
					h.run(false, outbox.ErrDeliveryNotCompleted)
					h.status(id, outbox.StateReconciliationRequired, 1)
				case "accepted_before_completion_commit":
					// Fail the second write, after the intent became completed inside
					// the transaction. Both writes must roll back together.
					h.exec(`CREATE TRIGGER interrupt_completion AFTER UPDATE ON outbound_attempts WHEN NEW.outcome='accepted' BEGIN SELECT RAISE(FAIL, 'private-storage-marker'); END`)
					h.fake.Enqueue(acceptedResponse())
					h.run(false, outbox.ErrStorage)
					h.status(id, outbox.StateAttempting, 1)
				}

				h.requests(1)
				h.restart(nil)
				status := h.status(id, outbox.StateReconciliationRequired, 1)

				if !status.NextAttemptAt.IsZero() || !status.TerminalAt.IsZero() {
					t.Fatalf("uncertain delivery has automatic retry or terminal time: %+v", status)
				}

				var finished sql.NullInt64
				var outcome sql.NullString

				if err := h.db.QueryRowContext(context.Background(), `SELECT finished_at,outcome FROM outbound_attempts WHERE delivery_id=?`, id).Scan(&finished, &outcome); err != nil {
					t.Fatal(err)
				}

				if interruption != "connection_drop" && (finished.Valid || outcome.Valid) {
					t.Fatal("interrupted transaction changed original attempt facts")
				}

				if interruption == "connection_drop" && (!finished.Valid || outcome.String != string(runtimecontract.DeliveryIndeterminate)) {
					t.Fatalf("dropped connection must preserve indeterminate evidence: %v %v", finished, outcome)
				}

				if err := h.store.ReconcilePending(context.Background(), time.Second); err != nil {
					t.Fatal(err)
				}

				h.clock.Advance(time.Hour)
				h.run(false, nil)
				h.requests(1) // No registered owner: restart never authorizes resend.
				h.status(id, outbox.StateReconciliationRequired, 1)

				if interruption == "accepted_before_completion_commit" {
					h.exec("DROP TRIGGER interrupt_completion")
				}
				calls := 0
				h.restart(recoveryReconciler(func(_ context.Context, delivery outbox.Delivery) (outbox.ReconciliationResolution, error) {
					calls++

					if delivery.ID != id || delivery.Attempts != 1 || string(delivery.Payload.Data()) != recoveryBody {
						t.Fatal("reconciliation lost original durable delivery")
					}

					return outbox.ReconciliationResolution{Decision: resolution, CorrelationID: "0123456789abcdef0123456789abcdef"}, nil
				}))
				if err := h.store.ReconcilePending(context.Background(), time.Second); err != nil {
					t.Fatal(err)
				}

				if calls != 1 {
					t.Fatalf("registered reconciliation calls = %d", calls)
				}

				h.requests(1) // A reconciliation decision itself never sends.
				if resolution == outbox.ReconciliationRetry {
					h.status(id, outbox.StatePending, 1)
					h.fake.Enqueue(acceptedResponse())
					h.run(true, nil)
					h.status(id, outbox.StateCompleted, 2)
					h.requests(2)
				} else {
					h.status(id, outbox.StateCompleted, 1)
					h.run(false, nil)
					h.requests(1)
				}
				h.restart(nil)
				h.run(false, nil)
				h.assertPrivate(h.diagnostics.String())
			})
		}
	}
}

func TestDeliveryRecoveryIntegrationTerminalStatus(t *testing.T) {
	h := newDeliveryRecovery(t)
	permanent := h.enqueue(3)
	h.fake.Enqueue(apiResponse(`{"ok":false,"error_code":403,"description":"private-api-marker"}`))
	h.run(false, outbox.ErrDeliveryNotCompleted)
	if s := h.status(permanent, outbox.StatePermanentFailure, 1); s.ReasonCode != "authorization" {
		t.Fatalf("permanent failure reason = %q", s.ReasonCode)
	}

	exhausted := h.enqueue(2)
	h.fake.Enqueue(apiResponse(`{"ok":false,"error_code":503,"description":"private-api-marker"}`))
	h.run(false, outbox.ErrDeliveryNotCompleted)
	h.restart(nil)
	h.run(false, nil)
	h.requests(2)
	h.clock.Advance(time.Second)
	h.fake.Enqueue(apiResponse(`{"ok":false,"error_code":503,"description":"private-api-marker"}`))
	h.run(false, outbox.ErrDeliveryNotCompleted)
	if s := h.status(exhausted, outbox.StatePermanentFailure, 2); s.ReasonCode != "retry_exhausted" {
		t.Fatalf("exhaustion reason = %q", s.ReasonCode)
	}

	completed := h.enqueue(3)
	h.fake.Enqueue(acceptedResponse())
	h.run(true, nil)
	retrying := h.enqueue(3)
	h.fake.Enqueue(apiResponse(`{"ok":false,"error_code":429,"parameters":{"retry_after":17}}`))
	h.run(false, outbox.ErrDeliveryNotCompleted)
	uncertain := h.enqueue(3)
	h.fake.Enqueue(testsupport.TelegramResponse{DropConnection: true})
	h.run(false, outbox.ErrDeliveryNotCompleted)
	pending := h.enqueue(3)
	h.restart(nil)
	h.status(completed, outbox.StateCompleted, 1)
	h.status(retrying, outbox.StateRetrying, 1)
	h.status(uncertain, outbox.StateReconciliationRequired, 1)
	h.status(pending, outbox.StatePending, 0)

	for state, want := range map[string]int{"pending": 1, "retrying": 1, "reconciliation_required": 1, "completed": 1, "permanent_failure": 2} {
		var got int
		if err := h.db.QueryRowContext(context.Background(), `SELECT count(*) FROM outbound_deliveries WHERE state=?`, state).Scan(&got); err != nil || got != want {
			t.Fatalf("state %s count=%d want=%d: %v", state, got, want, err)
		}
	}

	// Consume pending and retrying records, then prove terminal records remain
	// unclaimable across a further restart, even far beyond every due time.
	h.fake.Enqueue(acceptedResponse())
	h.run(true, nil)
	h.clock.Advance(time.Hour)
	h.fake.Enqueue(acceptedResponse())
	h.run(true, nil)
	h.restart(nil)
	h.run(false, nil)
	h.requests(8)
	h.status(permanent, outbox.StatePermanentFailure, 1)
	h.status(exhausted, outbox.StatePermanentFailure, 2)
	h.status(uncertain, outbox.StateReconciliationRequired, 1)
}

type deliveryRecovery struct {
	t           *testing.T
	dir         string
	db          *persistence.DB
	store       *outbox.Store
	worker      *outbox.Worker
	fake        *testsupport.FakeTelegram
	clock       *testsupport.ManualClock
	delivery    *telegram.Delivery
	diagnostics bytes.Buffer
}

func newDeliveryRecovery(t *testing.T) *deliveryRecovery {
	t.Helper()
	h := &deliveryRecovery{t: t, dir: t.TempDir(), clock: testsupport.NewManualClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))}
	h.fake = testsupport.NewFakeTelegram(apiResponse(`{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"Test"}}`))
	t.Cleanup(h.fake.Close)

	client, err := telegram.NewClient(context.Background(), telegram.ClientConfig{Token: "42:private-token-marker", BaseURL: h.fake.URL(), HTTPClient: h.fake.Client(), RequestTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	h.delivery = telegram.NewDelivery(client, h.clock.Now)
	t.Cleanup(func() {
		if h.db != nil {
			if err := h.db.Close(); err != nil {
				t.Error(err)
			}
		}
	})

	h.restart(nil)
	return h
}

func (h *deliveryRecovery) restart(reconciler outbox.Reconciler) {
	h.t.Helper()
	if h.db != nil {
		if err := h.db.Close(); err != nil {
			h.t.Fatal(err)
		}
	}
	db, err := persistence.Open(context.Background(), persistence.Options{StorageDir: h.dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		h.t.Fatal(err)
	}
	h.db = db
	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		h.t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), migrations); err != nil {
		h.t.Fatal(err)
	}
	h.store = outbox.NewStore(db, outbox.Options{Now: h.clock.Now, Diagnostics: observability.NewJSONSink(&h.diagnostics)})
	if err := h.store.Register("integration-public", 1, func(payload runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) {
		return runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -123, Kind: runtimecontract.ChatKindGroup}, Content: runtimecontract.MessageContent{Text: string(payload.Data())}}, nil
	}); err != nil {
		h.t.Fatal(err)
	}
	if reconciler != nil {
		if err := h.store.RegisterReconciler("integration-public", 1, reconciler); err != nil {
			h.t.Fatal(err)
		}
	}
	h.store.Seal()
	if err := h.store.Recover(context.Background()); err != nil {
		h.t.Fatal(err)
	}
	h.worker, err = outbox.NewWorker(h.store, outbox.WorkerOptions{Delivery: recoveryTelegramPort{h.delivery}, Sleep: h.clock.Sleep})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *deliveryRecovery) enqueue(maxAttempts int) runtimecontract.DeliveryID {
	h.t.Helper()
	payload, err := runtimecontract.NewVersionedPayload(1, []byte(recoveryBody))
	if err != nil {
		h.t.Fatal(err)
	}
	var id runtimecontract.DeliveryID

	err = h.db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		id, err = h.store.Enqueue(ctx, tx, runtimecontract.DeliveryRequest{Kind: "integration-public", Payload: payload, Retry: runtimecontract.RetryPolicy{MaxAttempts: maxAttempts, InitialDelay: time.Second, MaxDelay: 10 * time.Second}})
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return id
}

func (h *deliveryRecovery) status(id runtimecontract.DeliveryID, state outbox.State, attempts int) outbox.DeliveryStatus {
	h.t.Helper()
	s, err := h.store.Status(context.Background(), id)
	if err != nil || s.State != state || s.Attempts != attempts {
		h.t.Fatalf("delivery status = %+v, %v; want %s with %d attempts", s, err, state, attempts)
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		h.t.Fatal(err)
	}
	h.assertPrivate(string(encoded))
	return s
}

func (h *deliveryRecovery) run(want bool, wantErr error) {
	h.t.Helper()
	got, err := h.worker.RunOnce(context.Background())
	if got != want || !errors.Is(err, wantErr) {
		h.t.Fatalf("RunOnce = %v, %v; want %v, %v", got, err, want, wantErr)
	}
	if err != nil {
		h.assertPrivate(err.Error())
	}
}

func (h *deliveryRecovery) requests(want int) {
	h.t.Helper()
	requests := h.fake.Requests()
	if len(requests) != want+1 {
		h.t.Fatalf("transport calls = %d; want %d sends plus initial identity check", len(requests), want)
	}
	for _, request := range requests[1:] {
		if request.Path != "/bot42:private-token-marker/sendMessage" {
			h.t.Fatal("unexpected transport operation")
		}
	}
}

func (h *deliveryRecovery) exec(query string) {
	h.t.Helper()
	if _, err := h.db.ExecContext(context.Background(), query); err != nil {
		h.t.Fatal(err)
	}
}

func (h *deliveryRecovery) assertPrivate(value string) {
	h.t.Helper()
	for _, marker := range []string{recoveryBody, "private-token-marker", "private-api-marker", "private-storage-marker", h.dir, "integration-public"} {
		if strings.Contains(value, marker) {
			h.t.Fatal("operational output exposed private delivery data")
		}
	}
}

type recoveryTelegramPort struct{ delivery *telegram.Delivery }

func (p recoveryTelegramPort) Deliver(ctx context.Context, response runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
	receipt, err := p.delivery.SendPublic(ctx, response.(runtimecontract.PublicResponse))
	return telegram.Outcome(receipt, err)
}

type recoveryReconciler func(context.Context, outbox.Delivery) (outbox.ReconciliationResolution, error)

func (f recoveryReconciler) Reconcile(ctx context.Context, delivery outbox.Delivery) (outbox.ReconciliationResolution, error) {
	return f(ctx, delivery)
}

func apiResponse(body string) testsupport.TelegramResponse {
	return testsupport.TelegramResponse{Body: []byte(body)}
}

func acceptedResponse() testsupport.TelegramResponse {
	return apiResponse(`{"ok":true,"result":{"message_id":9,"chat":{"id":-123,"type":"group"}}}`)
}
