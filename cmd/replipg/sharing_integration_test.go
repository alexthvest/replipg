package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/sharing"
	"github.com/alexthvest/replipg/internal/telegram"
)

func sharingCompositionDB(t *testing.T) *persistence.DB {
	t.Helper()
	db, err := persistence.Open(t.Context(), persistence.Options{StorageDir: t.TempDir(), StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(t.Context(), migrations); err != nil {
		t.Fatal(err)
	}

	return db
}

func sharingTestRegistrations(t *testing.T, registry *interaction.Registry) app.Registrations {
	t.Helper()
	return app.Registrations{Registry: registry, Outbox: outbox.NewStore(sharingCompositionDB(t), outbox.Options{}), Membership: unavailableMembership{}}
}

func TestSharingLedgerEvidenceAndKindGuards(t *testing.T) {
	db := sharingCompositionDB(t)
	now := time.Unix(1000, 0).UTC()
	store := outbox.NewStore(db, outbox.Options{Now: func() time.Time { return now }})
	ledger, err := newSharingLedger(store)
	if err != nil {
		t.Fatal(err)
	}

	store.Seal()
	err = db.WithinTransaction(t.Context(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		request, err := sharing.NewPublicRequest(sharing.PublicPayload{ActionUpdateID: 1, Destination: runtimecontract.GroupDestination{ChatID: -100, Kind: runtimecontract.ChatKindSupergroup}, Content: runtimecontract.MessageContent{Text: "snapshot"}})
		if err != nil {
			return err
		}

		id, err := ledger.Enqueue(ctx, tx, request)
		if err != nil {
			return err
		}

		for _, tc := range []struct {
			state outbox.State
			want  sharing.DeliveryState
		}{
			{outbox.StatePending, sharing.Pending}, {outbox.StateAttempting, sharing.Pending}, {outbox.StateRetrying, sharing.Pending},
			{outbox.StateCompleted, sharing.Completed}, {outbox.StatePermanentFailure, sharing.Rejected}, {outbox.StateReconciliationRequired, sharing.Unknown},
		} {
			var next, terminal any

			if tc.state == outbox.StatePending || tc.state == outbox.StateRetrying {
				next = now.UnixMilli()
			}
			if tc.state == outbox.StateCompleted || tc.state == outbox.StatePermanentFailure {
				terminal = now.UnixMilli()
			}
			if _, err := tx.ExecContext(ctx, `UPDATE outbound_deliveries SET state=?, next_attempt_at=?, terminal_at=?, reason_code='permanent', updated_at=? WHERE id=?`, tc.state, next, terminal, now.UnixMilli(), id); err != nil {
				return err
			}

			evidence, err := ledger.Inspect(ctx, tx, id)
			if err != nil {
				return err
			}

			if evidence.State != tc.want || !evidence.UpdatedAt.Equal(now) || (terminal != nil && !evidence.TerminalAt.Equal(now)) {
				t.Fatalf("state %s: %+v", tc.state, evidence)
			}
		}

		if _, err := ledger.Inspect(ctx, tx, "missing"); !errors.Is(err, outbox.ErrNotFound) {
			t.Fatalf("missing evidence: %v", err)
		}

		request.Kind = "unrelated"
		if _, err := ledger.Enqueue(ctx, tx, request); err == nil {
			t.Fatal("unrelated enqueue accepted")
		}

		if _, err := ledger.Purge(ctx, tx, "unrelated", id, now); err == nil {
			t.Fatal("unrelated purge accepted")
		}

		if _, err := ledger.Purge(ctx, tx, sharing.FeedbackKind, id, now); !errors.Is(err, outbox.ErrConflict) {
			t.Fatalf("kind mismatch: %v", err)
		}

		if removed, err := ledger.Purge(ctx, tx, sharing.PublicKind, id, now.Add(-time.Hour)); err != nil || removed {
			t.Fatalf("young purge: %v %v", removed, err)
		}

		if removed, err := ledger.Purge(ctx, tx, sharing.PublicKind, id, now); err != nil || !removed {
			t.Fatalf("resolved purge: %v %v", removed, err)
		}

		if removed, err := ledger.Purge(ctx, tx, sharing.PublicKind, id, now); err != nil || !removed {
			t.Fatalf("idempotent purge: %v %v", removed, err)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSharingProductionDependencyFailures(t *testing.T) {
	if _, err := newSharingLedger(nil); err == nil {
		t.Fatal("missing outbox accepted")
	}

	for _, sealed := range []bool{false, true} {
		store := outbox.NewStore(sharingCompositionDB(t), outbox.Options{})
		if sealed {
			store.Seal()
		} else if _, err := newSharingLedger(store); err != nil {
			t.Fatal(err)
		}
		if _, err := newSharingLedger(store); err == nil {
			t.Fatal("schema registration failure ignored")
		}
	}

	// A conflict on the second schema must also prevent startup.
	store := outbox.NewStore(sharingCompositionDB(t), outbox.Options{})
	if err := store.Register(sharing.FeedbackKind, sharing.PayloadVersion, sharing.DecodeFeedback); err != nil {
		t.Fatal(err)
	}

	if _, err := newSharingLedger(store); !errors.Is(err, outbox.ErrRegistrationConflict) {
		t.Fatalf("feedback schema conflict: %v", err)
	}

	options, err := productionOptions(config.Config{}, commandLookup(nil))
	if err != nil {
		t.Fatal(err)
	}

	if err := options.Register(app.Registrations{Registry: interaction.NewRegistry(), Membership: unavailableMembership{}}); err == nil {
		t.Fatal("missing production outbox accepted")
	}

	for _, factory := range options.Workers {
		if factory.Name == "menu_sharing" {
			if _, err := factory.New(sharingCompositionDB(t), nil); err == nil {
				t.Fatal("factory accepted missing registration")
			}
		}
	}

	if err := options.Register(sharingTestRegistrations(t, interaction.NewRegistry())); err != nil {
		t.Fatal(err)
	}

	for _, factory := range options.Workers {
		if factory.Name == "menu_sharing" {
			if _, err := factory.New(nil, nil); err == nil {
				t.Fatal("factory accepted missing transactor")
			}
		}
	}

	options, err = productionOptions(config.Config{}, commandLookup(nil))
	if err != nil {
		t.Fatal(err)
	}

	if err := options.Register(sharingTestRegistrations(t, nil)); err == nil {
		t.Fatal("missing callback registry accepted")
	}
}

type sharingProductionScope struct {
	tx    runtimecontract.DBTX
	store *outbox.Store
}

func (s sharingProductionScope) DBTX() runtimecontract.DBTX { return s.tx }

func (s sharingProductionScope) EnqueueDelivery(ctx context.Context, req runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	return s.store.Enqueue(ctx, s.tx, req)
}

func (sharingProductionScope) Schedule(context.Context, runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	return "", errors.New("unexpected schedule")
}

type sharingProductionDelivery struct{ sender *telegram.Delivery }

func (p sharingProductionDelivery) Deliver(ctx context.Context, response runtimecontract.ResponseRequest) runtimecontract.DeliveryOutcome {
	var receipt runtimecontract.DeliveryReceipt
	var err error

	switch request := response.(type) {
	case runtimecontract.PublicResponse:
		receipt, err = p.sender.SendPublic(ctx, request)
	case runtimecontract.EphemeralResponse:
		receipt, err = p.sender.SendEphemeral(ctx, request)
	default:
		return runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryPermanentFailure}
	}
	return telegram.Outcome(receipt, err)
}

func TestSharingProductionCharacterPublicAndPersonalOutcome(t *testing.T) {
	options, err := productionOptions(config.Config{}, commandLookup(nil))
	if err != nil {
		t.Fatal(err)
	}

	db := sharingCompositionDB(t)
	if err := db.WithinTransaction(t.Context(), options.Prepare); err != nil {
		t.Fatal(err)
	}

	store := outbox.NewStore(db, outbox.Options{})
	registry := interaction.NewRegistry()
	if err := options.Register(app.Registrations{Registry: registry, Outbox: store, Membership: unavailableMembership{}}); err != nil {
		t.Fatal(err)
	}

	var projector app.Worker

	for _, factory := range options.Workers {
		if factory.Name == "menu_sharing" {
			projector, err = factory.New(db, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	if projector == nil {
		t.Fatal("missing sharing projector")
	}

	registry.Seal()
	store.Seal()
	requests := make(chan map[string]string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123,"is_bot":true}}`)
			return
		}

		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			t.Error("unexpected Telegram method")
			w.WriteHeader(400)
			return
		}

		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }()

		fields := map[string]string{}
		for key := range r.MultipartForm.Value {
			fields[key] = r.FormValue(key)
		}

		requests <- fields
		if fields["ephemeral_message_parameters"] != "" {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"ephemeral_message_id":900,"chat":{"id":-100,"type":"supergroup"}}}`)
		} else {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":901,"chat":{"id":-100,"type":"supergroup"}}}`)
		}
	}))
	defer server.Close()

	client, err := telegram.NewClient(t.Context(), telegram.ClientConfig{Token: "42:fixture", BaseURL: server.URL, HTTPClient: server.Client(), RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	sender := telegram.NewDelivery(client, time.Now)
	dispatch := func(event interaction.Event) interaction.HandlerResult {
		t.Helper()
		handler, ok := registry.Resolve(event)

		if !ok {
			t.Fatal("unregistered production route")
		}

		var result interaction.HandlerResult

		if err := db.WithinTransaction(t.Context(), func(ctx context.Context, tx runtimecontract.DBTX) error {
			var err error

			result, err = handler.Handle(ctx, sharingProductionScope{tx, store}, event)
			return err
		}); err != nil {
			t.Fatal(err)
		}

		return result
	}
	identity := interaction.Identity{Update: 1, User: 7, Chat: -100, ChatKind: runtimecontract.ChatKindSupergroup}
	opened := dispatch(interaction.Command{Identity: identity, Name: "character", EphemeralMessageID: 1, SentAt: time.Now()})
	personal := opened.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

	if len(personal.Content.Buttons) != 1 || personal.Content.Buttons[0][0].Text != "Share" {
		t.Fatal("production character lacks Share")
	}

	if _, err := sender.SendEphemeral(t.Context(), personal); err != nil {
		t.Fatal(err)
	}

	fields := <-requests

	if fields["ephemeral_message_parameters"] == "" {
		t.Fatal("character browsing became public")
	}

	var before int

	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM outbound_deliveries`).Scan(&before); err != nil || before != 0 {
		t.Fatal("browsing queued public work")
	}

	body := personal.Content
	body.Buttons = nil
	data := personal.Content.Buttons[0][0].CallbackData
	identity.Update = 2
	cb := interaction.Callback{Identity: identity, QueryID: "share-query", EphemeralMessageID: 900, Data: data, Source: &interaction.CallbackSource{Body: body, CallbackData: []string{data}, EphemeralMessageID: 900}}
	accepted := dispatch(cb)

	if len(accepted.ImmediateResponses) != 0 {
		t.Fatal("share emitted preparation guidance")
	}

	worker, err := outbox.NewWorker(store, outbox.WorkerOptions{Delivery: sharingProductionDelivery{sender}})
	if err != nil {
		t.Fatal(err)
	}

	if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("public worker: %v %v", worked, err)
	}

	fields = <-requests

	if fields["text"] != body.Text || fields["chat_id"] != "-100" || fields["ephemeral_message_parameters"] != "" || fields["reply_markup"] != "" || fields["reply_parameters"] != "" {
		t.Fatalf("invalid public snapshot fields: %v", fields)
	}

	if worked, err := projector.RunOnce(t.Context()); err != nil || !worked {
		t.Fatalf("projection: %v %v", worked, err)
	}

	if worked, err := worker.RunOnce(t.Context()); err != nil || worked {
		t.Fatalf("successful share queued feedback: %v %v", worked, err)
	}

	if replay := dispatch(cb); len(replay.ImmediateResponses) != 0 {
		t.Fatal("accepted replay produced another action")
	}

	var publicCount int

	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM outbound_deliveries WHERE kind='menu_share_public'`).Scan(&publicCount); err != nil || publicCount != 1 {
		t.Fatal("replay requested another public post")
	}

	var characters, inventories int

	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM characters`).Scan(&characters); err != nil {
		t.Fatal(err)
	}

	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM inventories`).Scan(&inventories); err != nil {
		t.Fatal(err)
	}

	if characters != 1 || inventories != 0 {
		t.Fatal("Share changed gameplay initialization")
	}
}

func TestSharingProductionWorkerComposition(t *testing.T) {
	options, err := productionOptions(config.Config{}, commandLookup(nil))
	if err != nil {
		t.Fatal(err)
	}

	if len(options.Workers) != 2 || options.Workers[0].Name != "group_menu" || options.Workers[1].Name != "menu_sharing" {
		t.Fatal("production composition lacks supervised menu_sharing worker")
	}
}

func TestSharingProductionExcludesSettingsAndDevelopment(t *testing.T) {
	options, err := productionOptions(config.Config{}, commandLookup(map[string]string{envDevCharacter: "true", envDevInventory: "true", envOwnerTelegramUserID: "7"}))
	if err != nil {
		t.Fatal(err)
	}

	db := sharingCompositionDB(t)
	if err := db.WithinTransaction(t.Context(), options.Prepare); err != nil {
		t.Fatal(err)
	}

	store := outbox.NewStore(db, outbox.Options{})
	registry := interaction.NewRegistry()
	if err := options.Register(app.Registrations{Registry: registry, Outbox: store, Membership: unavailableMembership{}}); err != nil {
		t.Fatal(err)
	}

	registry.Seal()
	for i, tc := range []struct {
		name      interaction.CommandName
		arguments string
		share     bool
	}{
		{"character", "", true}, {"inventory", "", true},
		{"settings", "", false}, {"dev_character_damage", "1", false},
		{"dev_character_defeat", "", false}, {"dev_inventory_grant", "coin 1", false},
		{"dev_inventory_heal", "healing_potion", false},
	} {
		command := interaction.Command{Identity: interaction.Identity{Update: runtimecontract.UpdateID(i + 1), User: 7, Chat: -100, ChatKind: runtimecontract.ChatKindSupergroup}, Name: tc.name, Arguments: tc.arguments, EphemeralMessageID: 1, SentAt: time.Now()}
		handler, ok := registry.Resolve(command)

		if !ok {
			t.Fatalf("missing %s route", tc.name)
		}

		if preparing, ok := handler.(interaction.PreparingHandler); ok {
			handler, err = preparing.Prepare(t.Context(), command)
			if err != nil {
				t.Fatal(err)
			}
		}

		if err := db.WithinTransaction(t.Context(), func(ctx context.Context, tx runtimecontract.DBTX) error {
			result, err := handler.Handle(ctx, sharingProductionScope{tx, store}, command)
			if err != nil {
				return err
			}

			if len(result.ImmediateResponses) != 1 {
				t.Fatalf("missing %s response", tc.name)
			}

			response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
			found := false
			for _, row := range response.Content.Buttons {
				for _, button := range row {
					found = found || strings.HasPrefix(button.CallbackData, "share:v1:")
				}
			}

			if found != tc.share {
				t.Fatalf("%s Share = %v, want %v", tc.name, found, tc.share)
			}

			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
