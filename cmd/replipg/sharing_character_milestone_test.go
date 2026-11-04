package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/telegram"
)

// This journey supplements the registration/outcome tests with repeated use of
// one actual displayed character view. All HTTP traffic stays on the local fake.
func TestSharingCharacterMilestone(t *testing.T) {
	options, err := productionOptions(config.Config{}, commandLookup(map[string]string{envDevCharacter: "true"}))
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
		t.Fatal("missing production sharing worker")
	}

	registry.Seal()
	store.Seal()
	requests := make(chan map[string]string, 32)
	var rejectPersonal atomic.Bool
	var publicSends atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123,"is_bot":true}}`)
			return
		}

		// Editing or deleting an earlier public copy is forbidden in this journey.
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
			if rejectPersonal.Load() {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"ok":false,"error_code":400,"description":"fixture rejection"}`)
				return
			}

			_, _ = io.WriteString(w, `{"ok":true,"result":{"ephemeral_message_id":900,"chat":{"id":-100,"type":"supergroup"}}}`)
		} else {
			id := 900 + publicSends.Add(1)
			_, _ = fmt.Fprintf(w, `{"ok":true,"result":{"message_id":%d,"chat":{"id":-100,"type":"supergroup"}}}`, id)
		}
	}))
	defer server.Close()

	client, err := telegram.NewClient(t.Context(), telegram.ClientConfig{Token: "42:fixture", BaseURL: server.URL, HTTPClient: server.Client(), RequestTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}

	sender := telegram.NewDelivery(client, time.Now)
	worker, err := outbox.NewWorker(store, outbox.WorkerOptions{Delivery: sharingProductionDelivery{sender}})
	if err != nil {
		t.Fatal(err)
	}

	receive := func() map[string]string {
		t.Helper()
		select {
		case fields := <-requests:
			return fields
		case <-time.After(time.Second):
			t.Fatal("missing fake Telegram request")
			return nil
		}
	}
	dispatch := func(event interaction.Event) interaction.HandlerResult {
		t.Helper()
		handler, ok := registry.Resolve(event)

		if !ok {
			t.Fatal("missing production route")
		}

		if preparing, ok := handler.(interaction.PreparingHandler); ok {
			handler, err = preparing.Prepare(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
		}

		var result interaction.HandlerResult

		if err := db.WithinTransaction(t.Context(), func(ctx context.Context, tx runtimecontract.DBTX) error {
			var handleErr error

			result, handleErr = handler.Handle(ctx, sharingProductionScope{tx, store}, event)
			return handleErr
		}); err != nil {
			t.Fatal(err)
		}

		return result
	}
	var update runtimecontract.UpdateID

	command := func(name interaction.CommandName, arguments string) runtimecontract.EphemeralResponse {
		t.Helper()
		update++
		result := dispatch(interaction.Command{Identity: interaction.Identity{Update: update, User: 7, Chat: -100, ChatKind: runtimecontract.ChatKindSupergroup}, Name: name, Arguments: arguments, EphemeralMessageID: 1, SentAt: time.Now()})

		if len(result.ImmediateResponses) != 1 {
			t.Fatalf("%s lacks personal response", name)
		}

		response, ok := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

		if !ok {
			t.Fatalf("%s became public", name)
		}

		return response
	}
	sendPersonal := func(response runtimecontract.EphemeralResponse, wantFailure bool) {
		t.Helper()
		_, err := sender.SendEphemeral(t.Context(), response)
		if (err != nil) != wantFailure {
			t.Fatalf("personal delivery failure=%v, want %v", err != nil, wantFailure)
		}

		if fields := receive(); fields["ephemeral_message_parameters"] == "" {
			t.Fatal("personal interaction sent publicly")
		}
	}
	open := command("character", "") // Deliberately no username in authenticated identity.
	if len(open.Content.Buttons) != 1 || open.Content.Buttons[0][0].Text != "Share" {
		t.Fatal("missing character Share")
	}

	sendPersonal(open, false)
	data := open.Content.Buttons[0][0].CallbackData
	press := func() interaction.HandlerResult {
		t.Helper()
		update++
		// Use the transport decoder rather than manufacture trusted callback evidence.
		raw, err := json.Marshal(map[string]any{"update_id": update, "callback_query": map[string]any{
			"id": fmt.Sprintf("milestone-%d", update), "from": map[string]any{"id": 7}, "data": data,
			"message": map[string]any{"ephemeral_message_id": 900, "date": time.Now().Unix(), "chat": map[string]any{"id": -100, "type": "supergroup"}, "text": open.Content.Text, "receiver_user": map[string]any{"id": 7}, "reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{{"text": "Share", "callback_data": data}}}}},
		}})
		if err != nil {
			t.Fatal(err)
		}

		decoded, err := telegram.DecodeUpdate(raw)
		if err != nil || decoded.Status != telegram.DecodeMapped {
			t.Fatalf("source callback decoding: %v %s", err, decoded.Status)
		}

		return dispatch(decoded.Event)
	}
	personalGuidance := func(result interaction.HandlerResult, fragment string) {
		t.Helper()

		if len(result.ImmediateResponses) != 1 {
			t.Fatal("missing personal sharing guidance")
		}

		response, ok := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

		if !ok || !strings.Contains(strings.ToLower(response.Content.Text), fragment) {
			t.Fatalf("unexpected personal guidance: %+v", result)
		}

		sendPersonal(response, false)
	}
	countPublic := func(want int) {
		t.Helper()
		var got int

		if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM outbound_deliveries WHERE kind='menu_share_public'`).Scan(&got); err != nil || got != want {
			t.Fatalf("public work=%d want %d: %v", got, want, err)
		}
	}
	run := func(w app.Worker) {
		t.Helper()
		if worked, err := w.RunOnce(t.Context()); err != nil || !worked {
			t.Fatalf("worker: worked=%v err=%v", worked, err)
		}
	}
	countPublic(0)
	personalGuidance(press(), "sharing")
	personalGuidance(press(), "in progress") // A distinct incoming update, not just a replay.
	countPublic(1)
	run(worker)
	first := receive()

	if first["text"] != open.Content.Text+"\n\nShared by user 7" || first["chat_id"] != "-100" || first["ephemeral_message_parameters"] != "" || first["reply_markup"] != "" || first["reply_parameters"] != "" {
		t.Fatalf("invalid attributed ordinary snapshot: %v", first)
	}

	// The blocked press scheduled the next inspection in the future. Make that
	// inspection due in the isolated database instead of waiting on wall time.
	if err := db.WithinTransaction(t.Context(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := tx.ExecContext(ctx, `UPDATE sharing_actions SET next_check_at=0 WHERE public_state='pending'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	run(projector)
	run(worker)
	if feedback := receive(); feedback["ephemeral_message_parameters"] == "" {
		t.Fatal("outcome guidance became public")
	}

	personalGuidance(press(), "second")
	countPublic(1)

	// Change live character state through the real development route, then browse
	// normally. Neither action may edit the old public snapshot or publish another.
	sendPersonal(command("dev_character_damage", "1"), false)
	latest := command("character", "")

	if latest.Content.Text == open.Content.Text {
		t.Fatal("fixture did not change live character state")
	}

	sendPersonal(latest, false)
	settings := command("settings", "")
	for _, response := range []runtimecontract.EphemeralResponse{settings, command("dev_character_damage", "0")} {
		for _, row := range response.Content.Buttons {
			for _, button := range row {
				if strings.HasPrefix(button.CallbackData, "share:v1:") {
					t.Fatal("excluded menu gained Share")
				}
			}
		}

		sendPersonal(response, false)
	}

	var before string
	const stateQuery = `SELECT json_array(id,user_id,max_health,health,attack,defense,recover_at_ms,recovery_health,revision) FROM characters WHERE user_id=7`

	if err := db.QueryRowContext(t.Context(), stateQuery).Scan(&before); err != nil {
		t.Fatal(err)
	}

	countPublic(1)

	// Production composition intentionally has no test clock knob. Age only this
	// isolated view's persisted successful outcome to the eligibility boundary;
	// exact before/at/after arithmetic is separately covered by core fake-clock tests.
	if err := db.WithinTransaction(t.Context(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := tx.ExecContext(ctx, `UPDATE sharing_views SET last_success_at=?,created_at=? WHERE token=?`, time.Now().Add(-30*time.Second).UnixMilli(), time.Now().Add(-24*time.Hour).UnixMilli(), strings.TrimPrefix(data, "share:v1:"))
		return err
	}); err != nil {
		t.Fatal(err)
	}

	personalGuidance(press(), "sharing") // Same old source, not a freshly rendered view.
	countPublic(2)
	run(worker)
	second := receive()

	if !reflect.DeepEqual(first, second) {
		t.Fatal("deliberate reshare substituted current character state or controls")
	}

	var after string

	if err := db.QueryRowContext(t.Context(), stateQuery).Scan(&after); err != nil || after != before {
		t.Fatal("sharing mutated character state")
	}

	var inventories int

	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM inventories`).Scan(&inventories); err != nil || inventories != 0 {
		t.Fatal("character sharing initialized inventory")
	}

	// A failed fresh personal view has no public fallback and does not remove the
	// two existing ordinary copies. Server rejects any edit/delete method above.
	rejectPersonal.Store(true)
	sendPersonal(command("character", ""), true)
	countPublic(2)

	if publicSends.Load() != 2 {
		t.Fatalf("public copies=%d, want two retained sends", publicSends.Load())
	}

	select {
	case <-requests:
		t.Fatal("unexpected fallback request")
	default:
	}
}
