package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

type processorHandler func(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error)

func (h processorHandler) Handle(c context.Context, s runtimecontract.WorkScope, e interaction.Event) (interaction.HandlerResult, error) {
	return h(c, s, e)
}

func processorRouter(t *testing.T, handler processorHandler, sink observability.DiagnosticSink) (*interaction.Router, *persistence.DB) {
	t.Helper()
	db, err := persistence.Open(context.Background(), persistence.Options{StorageDir: t.TempDir(), StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
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
	if err = db.Migrate(context.Background(), migrations); err != nil {
		t.Fatal(err)
	}
	registry := interaction.NewRegistry()
	if err = registry.RegisterCallback("action:", handler); err != nil {
		t.Fatal(err)
	}
	registry.Seal()
	return interaction.NewRouter(registry, db, db, nil, nil, interaction.RouterOptions{Diagnostics: sink, NewCorrelationID: func() (runtimecontract.CorrelationID, error) { return "0123456789abcdef0123456789abcdef", nil }}), db
}
func processorCallback(id runtimecontract.UpdateID) DecodedUpdate {
	return DecodedUpdate{UpdateID: id, Status: DecodeMapped, CallbackQueryID: "query", Event: interaction.Callback{Identity: interaction.Identity{Update: id, Chat: -100, User: 8, ChatKind: runtimecontract.ChatKindSupergroup}, QueryID: "query", Data: "action:run"}}
}

func TestProcessorAcknowledgesBeforeBlockedDispatch(t *testing.T) {
	for _, ackFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "failed"}[ackFailure], func(t *testing.T) {
			ack := `{"ok":true,"result":true}`
			if ackFailure {
				ack = `{"ok":false,"error_code":403,"description":"private-secret"}`
			}
			fake := testsupport.NewFakeTelegram(response(identity), response(ack))
			defer fake.Close()
			entered := make(chan struct{})
			release := make(chan struct{})
			defer close(release)
			var logs bytes.Buffer
			router, db := processorRouter(t, func(ctx context.Context, _ runtimecontract.WorkScope, _ interaction.Event) (interaction.HandlerResult, error) {
				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return interaction.HandlerResult{}, ctx.Err()
				}
				return interaction.HandlerResult{}, nil
			}, observability.NewJSONSink(&logs))
			p, err := NewProcessor(router, NewDelivery(newTestClient(t, fake), nil), ProcessorOptions{Diagnostics: observability.NewJSONSink(&logs)})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- p.ProcessUpdate(ctx, processorCallback(1)) }()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("processor returned before dispatch: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			requests := fake.Requests()
			if len(requests) != 2 || !strings.HasSuffix(requests[1].Path, "/answerCallbackQuery") {
				t.Fatal("callback not acknowledged before handler")
			}
			if cursor, err := db.CompletedUpdate(ctx); err != nil || cursor != 0 {
				t.Fatalf("cursor=%d err=%v", cursor, err)
			}
			release <- struct{}{}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(logs.String(), `"category":"callback_ack"`) || strings.Contains(logs.String(), "private-secret") {
				t.Fatalf("unsafe/missing diagnostics: %s", logs.String())
			}
		})
	}
}

func TestProcessorResponsesAfterCommitIsolateFailureAndPreserveVisibility(t *testing.T) {
	pollUpdate := func(id int) testsupport.TelegramResponse {
		return response(fmt.Sprintf(`{"ok":true,"result":[{"update_id":%d,"callback_query":{"id":"query","from":{"id":8},"message":{"message_id":9,"chat":{"id":-100,"type":"supergroup"}},"data":"action:run"}}]}`, id))
	}
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), pollUpdate(1), response(`{"ok":true,"result":true}`), response(`{"ok":false,"error_code":403,"description":"private-secret"}`), response(`{"ok":true,"result":{"message_id":9,"chat":{"id":-100,"type":"supergroup"}}}`), pollUpdate(2), response(`{"ok":true,"result":true}`), response(`{"ok":false,"error_code":409,"description":"private-secret"}`))
	defer fake.Close()
	var logs bytes.Buffer
	calls := 0
	router, db := processorRouter(t, func(ctx context.Context, s runtimecontract.WorkScope, _ interaction.Event) (interaction.HandlerResult, error) {
		calls++
		if _, err := s.DBTX().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS processor_feature(value INTEGER)`); err != nil {
			return interaction.HandlerResult{}, err
		}
		if _, err := s.DBTX().ExecContext(ctx, `INSERT INTO processor_feature VALUES (1)`); err != nil {
			return interaction.HandlerResult{}, err
		}
		if calls > 1 {
			return interaction.HandlerResult{}, nil
		}
		return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{runtimecontract.EphemeralResponse{Destination: publicRequest().Destination, TargetUserID: 8, Authorization: runtimecontract.AdministratorAuthorization{}, Content: runtimecontract.MessageContent{Text: "private-secret"}}, publicRequest()}}, nil
	}, observability.NewJSONSink(&logs))
	client := newTestClient(t, fake)
	p, err := NewProcessor(router, NewDelivery(client, nil), ProcessorOptions{Diagnostics: observability.NewJSONSink(&logs)})
	if err != nil {
		t.Fatal(err)
	}
	poller, err := NewPoller(client, db, PollerOptions{ProcessUpdate: p.ProcessUpdate})
	if err != nil {
		t.Fatal(err)
	}
	if err = poller.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = assertKind(t, poller.Run(context.Background()), ErrorConflict)
	var count int
	if err = db.QueryRowContext(context.Background(), `SELECT count(*) FROM processor_feature`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("committed count=%d err=%v", count, err)
	}
	requests := fake.Requests()
	if len(requests) != 9 {
		t.Fatalf("fallback/retry/suppression: %d requests", len(requests))
	}
	for i, method := range []string{"getMe", "deleteWebhook", "getUpdates", "answerCallbackQuery", "sendMessage", "sendMessage", "getUpdates", "answerCallbackQuery", "getUpdates"} {
		if !strings.HasSuffix(requests[i].Path, "/"+method) {
			t.Fatalf("request %d out of order", i)
		}
	}
	private := requestFields(t, requests[4])
	public := requestFields(t, requests[5])
	if private["ephemeral_message_parameters"] == "" || private["chat_id"] != "-100" || public["text"] != "Hello 😀" || public["ephemeral_message_parameters"] != "" {
		t.Fatal("response visibility/order changed")
	}
	if strings.Count(logs.String(), `"category":"delivery"`) != 2 || !strings.Contains(logs.String(), `"outcome":"failed"`) || strings.Contains(logs.String(), "private-secret") {
		t.Fatalf("diagnostics=%s", logs.String())
	}
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if !strings.Contains(line, `"correlation_id":"0123456789abcdef0123456789abcdef"`) {
			t.Fatalf("uncorrelated: %s", line)
		}
	}
}

func TestProcessorTerminalOutcomesAndSafeFailures(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`))
	defer fake.Close()
	router, db := processorRouter(t, func(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error) {
		return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{runtimecontract.PublicResponse{}}}, nil
	}, observability.NewJSONSink(nil))
	p, err := NewProcessor(router, NewDelivery(newTestClient(t, fake), nil), ProcessorOptions{Diagnostics: observability.NewJSONSink(nil)})
	if err != nil {
		t.Fatal(err)
	}
	for _, update := range []DecodedUpdate{processorCallback(1), {UpdateID: 2, Status: DecodeIgnored}, {UpdateID: 3, Status: DecodeFailed}} {
		if err = p.ProcessUpdate(context.Background(), update); err != nil {
			t.Fatal(err)
		}
	}
	for id, status := range map[runtimecontract.UpdateID]persistence.UpdateStatus{1: persistence.UpdateHandlerFailed, 2: persistence.UpdateIgnored, 3: persistence.UpdateDecodeFailed} {
		outcome, ok, err := db.TerminalOutcome(context.Background(), id)
		if err != nil || !ok || outcome.Status != status {
			t.Fatalf("outcome=%+v ok=%v err=%v", outcome, ok, err)
		}
	}
	if len(fake.Requests()) != 2 {
		t.Fatal("failed dispatch produced responses")
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	err = p.ProcessUpdate(context.Background(), DecodedUpdate{UpdateID: 4, Status: DecodeIgnored})
	if !errors.Is(err, ErrUpdateProcessing) || strings.Contains(err.Error(), "sqlite") {
		t.Fatalf("unsafe failure=%v", err)
	}
}

func TestProcessorConstructionAndInvalidInput(t *testing.T) {
	var router *interaction.Router
	var sender *Delivery
	if _, err := NewProcessor(router, sender, ProcessorOptions{}); !errors.Is(err, ErrInvalidProcessor) {
		t.Fatalf("err=%v", err)
	}
	fake := testsupport.NewFakeTelegram(response(identity))
	defer fake.Close()
	r, _ := processorRouter(t, func(context.Context, runtimecontract.WorkScope, interaction.Event) (interaction.HandlerResult, error) {
		t.Fatal("invalid input dispatched")
		return interaction.HandlerResult{}, nil
	}, observability.NewJSONSink(nil))
	d := NewDelivery(newTestClient(t, fake), nil)
	if _, err := NewProcessor(r, sender, ProcessorOptions{}); !errors.Is(err, ErrInvalidProcessor) {
		t.Fatalf("typed nil sender=%v", err)
	}
	p, err := NewProcessor(r, d, ProcessorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []DecodedUpdate{{Status: DecodeIgnored}, {UpdateID: 1, Status: "unknown"}, {UpdateID: 1, Status: DecodeMapped}, {UpdateID: 2, Status: DecodeMapped, Event: processorCallback(1).Event}} {
		if err = p.ProcessUpdate(context.Background(), u); !errors.Is(err, ErrUpdateProcessing) {
			t.Fatalf("invalid input accepted: %v", err)
		}
	}
	if len(fake.Requests()) != 1 {
		t.Fatal("invalid input caused I/O")
	}
}
