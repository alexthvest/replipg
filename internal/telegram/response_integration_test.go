package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func responseIntegrationCallback(id int) testsupport.TelegramResponse {
	return response(fmt.Sprintf(`{"ok":true,"result":[{"update_id":%d,"callback_query":{"id":"private-query","from":{"id":8},"message":{"message_id":9,"chat":{"id":-100,"type":"supergroup"}},"data":"act:private-input"}}]}`, id))
}

func responseIntegrationPoller(t *testing.T, db *persistence.DB, cursors persistence.CursorStore, baseURL string, httpClient *http.Client, handler processorHandler, logs *bytes.Buffer) *Poller {
	t.Helper()
	registry := interaction.NewRegistry()
	for _, err := range []error{registry.RegisterCallback("act:", handler), registry.RegisterCommand("act", handler)} {
		if err != nil {
			t.Fatal(err)
		}
	}

	registry.Seal()
	clock := testsupport.NewManualClock(time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC))
	sink := observability.NewJSONSink(logs)
	router := interaction.NewRouter(registry, db, cursors, nil, nil, interaction.RouterOptions{Now: clock.Now, Diagnostics: sink})
	client, err := NewClient(t.Context(), ClientConfig{Token: "42:secret", BaseURL: baseURL, HTTPClient: httpClient, RequestTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	processor, err := NewProcessor(router, NewDelivery(client, clock.Now), ProcessorOptions{Diagnostics: sink})
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

// Block only the first update's terminal transaction, including its failure
// transaction. This permits observing rollback before terminal acknowledgement.
type responseTerminalGate struct{ cursorCommitGate }

func (g responseTerminalGate) RecordTerminal(ctx context.Context, tx runtimecontract.DBTX, outcome persistence.UpdateOutcome) error {
	if outcome.UpdateID == 1 {
		return g.cursorCommitGate.RecordTerminal(ctx, tx, outcome)
	}

	return g.CursorStore.RecordTerminal(ctx, tx, outcome)
}

func assertResponseDiagnostics(t *testing.T, db *persistence.DB, logs string, want map[string][]string) {
	t.Helper()
	for _, secret := range []string{"42:secret", "private-query", "private-input", "private-output", "private-api", "private-handler"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("diagnostic exposed %q", secret)
		}
	}

	got := map[string][]string{}
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var diagnostic struct {
			Category    string                   `json:"category"`
			Correlation string                   `json:"correlation_id"`
			Outcome     string                   `json:"outcome"`
			UpdateID    runtimecontract.UpdateID `json:"update_id"`
		}

		if err := json.Unmarshal([]byte(line), &diagnostic); err != nil {
			t.Fatal(err)
		}

		if diagnostic.Category != "callback_ack" && diagnostic.Category != "dispatch" && diagnostic.Category != "delivery" {
			continue
		}

		// Dispatch identifies its own update. Only update 1 requests callback
		// acknowledgement or delivery in these scenarios, so those content-free
		// diagnostics must retain that update's durable correlation.
		updateID := diagnostic.UpdateID
		if diagnostic.Category != "dispatch" {
			updateID = 1
		}
		terminal, found, err := db.TerminalOutcome(t.Context(), updateID)
		if err != nil || !found {
			t.Fatalf("diagnostic has no matching terminal outcome for update %d: %s; error=%v", updateID, line, err)
		}

		if diagnostic.Correlation != string(terminal.CorrelationID) {
			t.Fatalf("uncorrelated diagnostic: %s", line)
		}

		got[diagnostic.Category] = append(got[diagnostic.Category], diagnostic.Outcome)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diagnostic outcomes=%v, want %v", got, want)
	}
}

func TestResponseIntegrationAcknowledgementAndHandlerFailureAreIndependent(t *testing.T) {
	for _, failAck := range []bool{false, true} {
		t.Run(fmt.Sprintf("ack_failure_%t", failAck), func(t *testing.T) {
			dir := t.TempDir()
			db := openCursorIntegrationDB(t, dir)
			observer := openCursorIntegrationDB(t, dir)
			ack := response(`{"ok":true,"result":true}`)
			if failAck {
				ack = response(`{"ok":false,"error_code":403,"description":"private-api"}`)
			}
			fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), responseIntegrationCallback(1), ack, cursorCommand(2), cursorStopResponse())
			defer fake.Close()

			entered, release := make(chan struct{}), make(chan struct{})
			gate := responseTerminalGate{cursorCommitGate{CursorStore: db, entered: make(chan struct{}), release: make(chan struct{})}}
			var logs bytes.Buffer

			poller := responseIntegrationPoller(t, db, gate, fake.URL(), fake.Client(), func(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
				result, err := cursorEffects(ctx, scope, event)
				if err != nil || event.UpdateID() != 1 {
					return result, err
				}

				close(entered)
				select {
				case <-release:
				case <-ctx.Done():
					return result, ctx.Err()
				}
				return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{publicRequest()}}, errors.New("private-handler")
			}, &logs)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- poller.Run(ctx) }()
			waitCursorGate(t, ctx, entered, done)
			requests := fake.Requests()

			if len(requests) != 4 || !strings.HasSuffix(requests[3].Path, "/answerCallbackQuery") {
				t.Fatalf("callback not acknowledged before blocked handler: %d requests", len(requests))
			}

			fields := requestFields(t, requests[3])

			if fields["callback_query_id"] != "private-query" || fields["text"] != "" || fields["show_alert"] == "true" {
				t.Fatal("acknowledgement was visible or lost identity")
			}

			assertCursorState(t, observer, 0, 0, 0)
			assertCursorRequests(t, fake, []int64{1})
			close(release)
			waitCursorGate(t, ctx, gate.entered, done)
			// Failure writes are still uncommitted, and feature writes have rolled back.
			assertCursorState(t, observer, 0, 0, 0)
			assertCursorRequests(t, fake, []int64{1})
			close(gate.release)
			_ = assertKind(t, waitCursorDone(t, ctx, done), ErrorConflict)
			assertCursorState(t, observer, 2, 1, 2)
			assertCursorRequests(t, fake, []int64{1, 2, 3})
			outcome, found, err := db.TerminalOutcome(ctx, 1)
			if err != nil || !found || outcome.Status != persistence.UpdateHandlerFailed || outcome.FailureCode != "handler_error" {
				t.Fatalf("wrong failed terminal outcome: %+v %v", outcome, err)
			}

			if len(fake.Requests()) != 6 {
				t.Fatal("failed handler emitted a response or fallback")
			}

			ackOutcome := "ok"
			if failAck {
				ackOutcome = "failed"
			}
			assertResponseDiagnostics(t, db, logs.String(), map[string][]string{"dispatch": {"failed", "completed"}, "callback_ack": {ackOutcome}})
		})
	}
}

func TestResponseIntegrationCommitPrecedesSequentialDeliveryAndNextPoll(t *testing.T) {
	for _, failedIndex := range []int{-1, 0, 1} {
		t.Run(fmt.Sprintf("failed_response_%d", failedIndex), func(t *testing.T) {
			dir := t.TempDir()
			db := openCursorIntegrationDB(t, dir)
			observer := openCursorIntegrationDB(t, dir)
			replies := []testsupport.TelegramResponse{
				response(`{"ok":true,"result":{"message_id":10,"chat":{"id":-100,"type":"supergroup"}}}`),
				response(`{"ok":true,"result":{"ephemeral_message_id":11,"chat":{"id":-100,"type":"supergroup"}}}`),
				response(`{"ok":true,"result":{"message_id":12,"chat":{"id":-100,"type":"supergroup"}}}`),
			}
			if failedIndex >= 0 {
				replies[failedIndex] = response(`{"ok":false,"error_code":403,"description":"private-api"}`)
			}
			fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), responseIntegrationCallback(1), response(`{"ok":true,"result":true}`))
			defer fake.Close()

			fake.Enqueue(replies...)
			fake.Enqueue(cursorCommand(2), cursorStopResponse())
			entered, release := make(chan struct{}), make(chan struct{})
			first := true
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/sendMessage") && first {
					first = false
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return
					}
				}
				fake.ServeHTTP(w, r)
			}))
			defer server.Close()

			var logs bytes.Buffer

			poller := responseIntegrationPoller(t, db, db, server.URL, server.Client(), func(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
				result, err := cursorEffects(ctx, scope, event)
				if err != nil || event.UpdateID() != 1 {
					return result, err
				}

				return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{
					publicRequest(),
					runtimecontract.EphemeralResponse{Destination: publicRequest().Destination, TargetUserID: 8, Authorization: runtimecontract.AdministratorAuthorization{}, Content: runtimecontract.MessageContent{Text: "private-output"}},
					publicRequest(),
				}}, nil
			}, &logs)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- poller.Run(ctx) }()
			waitCursorGate(t, ctx, entered, done)
			// An independent connection sees the commit while the first send is blocked.
			assertCursorState(t, observer, 1, 1, 1)
			assertCursorRequests(t, fake, []int64{1})

			if len(fake.Requests()) != 4 {
				t.Fatal("later response or next poll overtook blocked first response")
			}

			close(release)
			_ = assertKind(t, waitCursorDone(t, ctx, done), ErrorConflict)
			assertCursorState(t, observer, 2, 2, 2)
			assertCursorRequests(t, fake, []int64{1, 2, 3})
			requests := fake.Requests()

			if len(requests) != 9 {
				t.Fatalf("response suppressed or fallback added: %d requests", len(requests))
			}

			for i, method := range []string{"getMe", "deleteWebhook", "getUpdates", "answerCallbackQuery", "sendMessage", "sendMessage", "sendMessage", "getUpdates", "getUpdates"} {
				if !strings.HasSuffix(requests[i].Path, "/"+method) {
					t.Fatalf("request %d violates response sequence", i)
				}
			}

			for i := 0; i < 3; i++ {
				fields := requestFields(t, requests[i+4])

				if fields["chat_id"] != "-100" {
					t.Fatal("response left the group")
				}

				if i == 1 {
					var ephemeral map[string]any

					if err := json.Unmarshal([]byte(fields["ephemeral_message_parameters"]), &ephemeral); err != nil {
						t.Fatal(err)
					}

					if ephemeral["receiver_user_id"] != float64(8) || fields["text"] != "private-output" {
						t.Fatalf("restricted target/content lost: %v", ephemeral)
					}
				} else if fields["ephemeral_message_parameters"] != "" || fields["text"] != publicRequest().Content.Text {
					t.Fatal("public visibility/content changed")
				}
			}

			deliveryOutcomes := []string{"ok", "ok", "ok"}
			if failedIndex >= 0 {
				deliveryOutcomes[failedIndex] = "failed"
			}
			assertResponseDiagnostics(t, db, logs.String(), map[string][]string{"dispatch": {"completed", "completed"}, "callback_ack": {"ok"}, "delivery": deliveryOutcomes})
		})
	}
}

func TestResponseIntegrationInvalidRequestsRollbackBeforeDelivery(t *testing.T) {
	invalidDestination := publicRequest()
	invalidDestination.Destination.ChatID = 0
	invalidContent := publicRequest()
	invalidContent.Content.Text = ""
	invalidTarget := runtimecontract.EphemeralResponse{Destination: publicRequest().Destination, Authorization: runtimecontract.AdministratorAuthorization{}, Content: runtimecontract.MessageContent{Text: "private-output"}}
	for name, invalid := range map[string]runtimecontract.ResponseRequest{"destination": invalidDestination, "content": invalidContent, "target": invalidTarget} {
		t.Run(name, func(t *testing.T) {
			db := openCursorIntegrationDB(t, t.TempDir())
			fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), responseIntegrationCallback(1), response(`{"ok":true,"result":true}`), cursorCommand(2), cursorStopResponse())
			defer fake.Close()

			var logs bytes.Buffer

			poller := responseIntegrationPoller(t, db, db, fake.URL(), fake.Client(), func(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
				result, err := cursorEffects(ctx, scope, event)
				if err != nil || event.UpdateID() != 1 {
					return result, err
				}

				// The valid earlier request must also remain unsent when validation fails.
				return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{publicRequest(), invalid}}, nil
			}, &logs)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			_ = assertKind(t, poller.Run(ctx), ErrorConflict)
			assertCursorState(t, db, 2, 1, 2)
			assertCursorRequests(t, fake, []int64{1, 2, 3})
			outcome, found, err := db.TerminalOutcome(ctx, 1)
			if err != nil || !found || outcome.Status != persistence.UpdateHandlerFailed || outcome.FailureCode != "invalid_response" {
				t.Fatalf("invalid response was not terminally rejected: %+v %v", outcome, err)
			}

			if len(fake.Requests()) != 6 {
				t.Fatal("invalid response attempted delivery or fallback")
			}

			assertResponseDiagnostics(t, db, logs.String(), map[string][]string{"dispatch": {"failed", "completed"}, "callback_ack": {"ok"}})
		})
	}
}
