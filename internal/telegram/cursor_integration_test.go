package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

// Gate after all terminal writes but before the transaction callback returns.
// A second SQLite connection must still see neither effects nor completion.
type cursorCommitGate struct {
	persistence.CursorStore
	entered chan struct{}
	release chan struct{}
}

func (g cursorCommitGate) RecordTerminal(ctx context.Context, tx runtimecontract.DBTX, outcome persistence.UpdateOutcome) error {
	if err := g.CursorStore.RecordTerminal(ctx, tx, outcome); err != nil {
		return err
	}
	close(g.entered)
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func openCursorIntegrationDB(t *testing.T, dir string) *persistence.DB {
	t.Helper()
	db, err := persistence.Open(t.Context(), persistence.Options{StorageDir: dir, StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
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
	if err := db.Migrate(t.Context(), migrations); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE IF NOT EXISTS cursor_feature(update_id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func cursorIntegrationPoller(t *testing.T, db *persistence.DB, cursors persistence.CursorStore, fake *testsupport.FakeTelegram, handler processorHandler, clock *testsupport.ManualClock) *Poller {
	t.Helper()
	registry := interaction.NewRegistry()
	for _, err := range []error{registry.RegisterCommand("act", handler), registry.RegisterCallback("act:", handler), registry.RegisterGroupMessages(handler)} {
		if err != nil {
			t.Fatal(err)
		}
	}
	registry.Seal()
	sink := observability.NewJSONSink(io.Discard)
	router := interaction.NewRouter(registry, db, cursors, nil, nil, interaction.RouterOptions{Now: clock.Now, Diagnostics: sink})
	client, err := NewClient(t.Context(), ClientConfig{Token: "42:secret", BaseURL: fake.URL(), HTTPClient: fake.Client(), RequestTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	processor, err := NewProcessor(router, NewDelivery(client, nil), ProcessorOptions{Diagnostics: sink})
	if err != nil {
		t.Fatal(err)
	}
	poller, err := NewPoller(client, db, PollerOptions{TimeoutSeconds: 20, ProcessUpdate: processor.ProcessUpdate, Sleep: clock.Sleep})
	if err != nil {
		t.Fatal(err)
	}
	if err := poller.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	return poller
}

func cursorCommand(id int) testsupport.TelegramResponse {
	return response(fmt.Sprintf(`{"ok":true,"result":[{"update_id":%d,"message":{"message_id":9,"from":{"id":8},"chat":{"id":-100,"type":"supergroup"},"text":"/act","entities":[{"type":"bot_command","offset":0,"length":4}]}}]}`, id))
}

func cursorStopResponse() testsupport.TelegramResponse {
	return response(`{"ok":false,"error_code":409,"description":"another poller"}`)
}

func cursorEffects(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	_, err := scope.DBTX().ExecContext(ctx, `INSERT INTO cursor_feature(update_id) VALUES (?)`, int64(event.UpdateID()))
	return interaction.HandlerResult{}, err
}

func seedCursorIntegration(t *testing.T, db *persistence.DB, id runtimecontract.UpdateID) {
	t.Helper()
	err := db.WithinTransaction(t.Context(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		return db.RecordTerminal(ctx, tx, persistence.UpdateOutcome{UpdateID: id, Status: persistence.UpdateIgnored, CorrelationID: "prior-session", CompletedAt: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertCursorState(t *testing.T, db *persistence.DB, wantCursor runtimecontract.UpdateID, wantEffects, wantOutcomes int) {
	t.Helper()
	cursor, err := db.CompletedUpdate(t.Context())
	if err != nil || cursor != wantCursor {
		t.Fatalf("durable cursor=%d, want %d; error=%v", cursor, wantCursor, err)
	}
	for table, want := range map[string]int{"cursor_feature": wantEffects, "processed_updates": wantOutcomes} {
		var count int
		if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Errorf("%s count=%d, want %d", table, count, want)
		}
	}
}

func assertCursorRequests(t *testing.T, fake *testsupport.FakeTelegram, want []int64) {
	t.Helper()
	var offsets []int64
	for _, request := range fake.Requests() {
		if strings.HasSuffix(request.Path, "/deleteWebhook") && len(request.Body) > 0 {
			var webhook struct {
				DropPending bool `json:"drop_pending_updates"`
			}
			if err := json.Unmarshal(request.Body, &webhook); err != nil || webhook.DropPending {
				t.Fatalf("webhook backlog was not preserved: %v", err)
			}
		}
		if !strings.HasSuffix(request.Path, "/getUpdates") {
			continue
		}
		var poll GetUpdatesRequest
		if err := json.Unmarshal(request.Body, &poll); err != nil {
			t.Fatal(err)
		}
		if poll.Limit != 1 || poll.Timeout != 20 || !reflect.DeepEqual(poll.AllowedUpdates, []string{"message", "callback_query"}) {
			t.Fatalf("unsafe poll request: %+v", poll)
		}
		offsets = append(offsets, poll.Offset)
	}
	if !reflect.DeepEqual(offsets, want) {
		t.Fatalf("poll offsets=%v, want %v", offsets, want)
	}
}

func waitCursorGate(t *testing.T, ctx context.Context, entered <-chan struct{}, done <-chan error) {
	t.Helper()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("polling exited before reaching commit gate: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func waitCursorDone(t *testing.T, ctx context.Context, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return nil
	}
}

func TestCursorIntegrationCommitControlsAcknowledgementAndDuplicateSuppression(t *testing.T) {
	dir := t.TempDir()
	db := openCursorIntegrationDB(t, dir)
	seedCursorIntegration(t, db, 40)
	observer := openCursorIntegrationDB(t, dir)
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), cursorCommand(41), cursorCommand(41), cursorStopResponse())
	defer fake.Close()
	gate := cursorCommitGate{CursorStore: db, entered: make(chan struct{}), release: make(chan struct{})}
	var calls atomic.Int64
	poller := cursorIntegrationPoller(t, db, gate, fake, func(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
		calls.Add(1)
		command, ok := event.(interaction.Command)
		if !ok || command.Name != "act" || command.MessageID != 9 || command.UpdateID() != 41 || command.ChatID() != -100 || command.UserID() != 8 {
			t.Errorf("lost command identity: %+v", event)
		}
		return cursorEffects(ctx, scope, event)
	}, testsupport.NewManualClock(time.Now()))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- poller.Run(ctx) }()
	waitCursorGate(t, ctx, gate.entered, done)
	assertCursorState(t, observer, 40, 0, 1)
	assertCursorRequests(t, fake, []int64{41})
	close(gate.release)
	_ = assertKind(t, waitCursorDone(t, ctx, done), ErrorConflict)
	assertCursorState(t, observer, 41, 1, 2)
	assertCursorRequests(t, fake, []int64{41, 42, 42})
	if calls.Load() != 1 {
		t.Fatalf("completed duplicate repeated handler: calls=%d", calls.Load())
	}
}

func TestCursorIntegrationCancellationBeforeCommitReopensAndRedelivers(t *testing.T) {
	dir := t.TempDir()
	db := openCursorIntegrationDB(t, dir)
	seedCursorIntegration(t, db, 90)
	clock := testsupport.NewManualClock(time.Now())
	first := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), cursorCommand(91))
	defer first.Close()
	gate := cursorCommitGate{CursorStore: db, entered: make(chan struct{}), release: make(chan struct{})}
	var attempts atomic.Int64
	handler := func(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
		attempts.Add(1)
		return cursorEffects(ctx, scope, event)
	}
	poller := cursorIntegrationPoller(t, db, gate, first, handler, clock)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	work, cancelWork := context.WithCancel(ctx)
	defer cancelWork()
	done := make(chan error, 1)
	go func() { done <- poller.Run(work) }()
	waitCursorGate(t, ctx, gate.entered, done)
	cancelWork()
	if err := waitCursorDone(t, ctx, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled poller: %v", err)
	}
	assertCursorRequests(t, first, []int64{91})
	assertCursorState(t, db, 90, 0, 1)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openCursorIntegrationDB(t, dir)
	assertCursorState(t, reopened, 90, 0, 1)
	second := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), cursorCommand(91), cursorCommand(91), cursorStopResponse())
	defer second.Close()
	restarted := cursorIntegrationPoller(t, reopened, reopened, second, handler, clock)
	_ = assertKind(t, restarted.Run(ctx), ErrorConflict)
	assertCursorRequests(t, second, []int64{91, 92, 92})
	assertCursorState(t, reopened, 91, 1, 2)
	if attempts.Load() != 2 {
		t.Fatalf("handler attempts=%d, want rolled-back attempt and one successful redelivery", attempts.Load())
	}
}

func TestCursorIntegrationTransientPollingPreservesLaterInteractionsAndTerminalOutcomes(t *testing.T) {
	db := openCursorIntegrationDB(t, t.TempDir())
	seedCursorIntegration(t, db, 50)
	clock := testsupport.NewManualClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), cursorCommand(51),
		response(`{"ok":false,"error_code":500,"description":"temporary interruption"}`),
		response(`{"ok":true,"result":[{"update_id":52,"callback_query":{"id":"query-52","from":{"id":8},"message":{"message_id":9,"chat":{"id":-100,"type":"supergroup"}},"data":"act:next"}}]}`),
		response(`{"ok":true,"result":true}`),
		response(`{"ok":true,"result":[{"update_id":53,"message":{"message_id":10,"from":{"id":8},"chat":{"id":-100,"type":"supergroup"},"text":"ordinary message"}}]}`),
		response(`{"ok":true,"result":[{"update_id":54,"poll":{}}]}`),
		response(`{"ok":true,"result":[{"update_id":55,"message":null}]}`), cursorStopResponse())
	defer fake.Close()
	var events []interaction.Event
	poller := cursorIntegrationPoller(t, db, db, fake, func(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
		events = append(events, event)
		return cursorEffects(ctx, scope, event)
	}, clock)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- poller.Run(ctx) }()
	if err := clock.WaitForTimers(ctx, 1); err != nil {
		t.Fatal(err)
	}
	assertCursorState(t, db, 51, 1, 2)
	assertCursorRequests(t, fake, []int64{51, 52})
	clock.Advance(250 * time.Millisecond)
	_ = assertKind(t, waitCursorDone(t, ctx, done), ErrorConflict)
	assertCursorRequests(t, fake, []int64{51, 52, 52, 53, 54, 55, 56})
	assertCursorState(t, db, 55, 3, 6)
	if len(events) != 3 {
		t.Fatalf("routed %d events, want command, callback, ordinary message", len(events))
	}
	callback, ok := events[1].(interaction.Callback)
	if !ok || callback.UpdateID() != 52 || callback.ChatID() != -100 || callback.UserID() != 8 || callback.QueryID != "query-52" || callback.MessageID != 9 || callback.Data != "act:next" {
		t.Errorf("lost callback identity: %+v", events[1])
	}
	message, ok := events[2].(interaction.GroupMessage)
	if !ok || message.UpdateID() != 53 || message.ChatID() != -100 || message.UserID() != 8 || message.MessageID != 10 || message.Text != "ordinary message" {
		t.Errorf("lost message identity: %+v", events[2])
	}
	for id, want := range map[int]string{54: "ignored", 55: "decode_failed"} {
		var status string
		if err := db.QueryRowContext(ctx, `SELECT status FROM processed_updates WHERE update_id=?`, id).Scan(&status); err != nil || status != want {
			t.Errorf("update %d terminal status=%q, want %q; error=%v", id, status, want, err)
		}
	}
}
