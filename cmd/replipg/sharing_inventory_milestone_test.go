package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/outbox"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/telegram"
)

type inventoryMilestoneMembership struct{}

func (inventoryMilestoneMembership) VerifyGroupMember(_ context.Context, _ runtimecontract.ChatID, user runtimecontract.UserID) (runtimecontract.MembershipResult, error) {
	status := runtimecontract.Member
	if user == 7 {
		status = runtimecontract.Administrator
	}
	return runtimecontract.MembershipResult{Status: status, CheckedAt: time.Now()}, nil
}

type inventoryMilestoneMenus struct {
	commands []runtimecontract.CommandDeclaration
}

func (m *inventoryMilestoneMenus) ReplaceGroupMenu(_ context.Context, _ runtimecontract.ChatID, commands []runtimecontract.CommandDeclaration) error {
	m.commands = append([]runtimecontract.CommandDeclaration(nil), commands...)
	return nil
}

// This milestone exercises production composition. Exact clock edges and hostile
// callback source cases are covered by the service and inventory-source tests.
func TestSharingInventoryMilestone(t *testing.T) {
	for _, locale := range []string{"en", "ru"} {
		t.Run(locale, func(t *testing.T) {
			options, err := productionOptions(config.Config{}, commandLookup(nil))
			if err != nil {
				t.Fatal(err)
			}

			assertCommands := func(commands []runtimecontract.CommandDeclaration) {
				t.Helper()

				if len(commands) != 3 {
					t.Fatalf("command visibility changed: %+v", commands)
				}

				for i, name := range []string{"character", "inventory", "settings"} {
					if commands[i].Name != name || !commands[i].Ephemeral {
						t.Fatalf("command contract changed: %+v", commands)
					}
				}
			}
			assertCommands(options.Commands)
			db := sharingCompositionDB(t)
			if err := db.WithinTransaction(t.Context(), options.Prepare); err != nil {
				t.Fatal(err)
			}

			store := outbox.NewStore(db, outbox.Options{})
			registry := interaction.NewRegistry()
			if err := options.Register(app.Registrations{Registry: registry, Outbox: store, Membership: inventoryMilestoneMembership{}}); err != nil {
				t.Fatal(err)
			}

			var projector, menusWorker app.Worker

			menus := &inventoryMilestoneMenus{}
			for _, factory := range options.Workers {
				worker, err := factory.New(db, menus)
				if err != nil {
					t.Fatal(err)
				}

				switch factory.Name {
				case "menu_sharing":
					projector = worker
				case "group_menu":
					menusWorker = worker
				}
			}

			if projector == nil || menusWorker == nil {
				t.Fatal("missing production workers")
			}

			registry.Seal()
			store.Seal()
			requests := make(chan map[string]string, 64)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/getMe") {
					_, _ = io.WriteString(w, `{"ok":true,"result":{"id":123,"is_bot":true}}`)
					return
				}

				if !strings.HasSuffix(r.URL.Path, "/sendMessage") && !strings.HasSuffix(r.URL.Path, "/editEphemeralMessageText") {
					t.Error("unexpected Telegram method")
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = r.MultipartForm.RemoveAll() }()

				fields := map[string]string{"method": r.URL.Path}
				for key := range r.MultipartForm.Value {
					fields[key] = r.FormValue(key)
				}

				requests <- fields
				if strings.HasSuffix(r.URL.Path, "/editEphemeralMessageText") {
					_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
				} else if fields["ephemeral_message_parameters"] != "" {
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
			worker, err := outbox.NewWorker(store, outbox.WorkerOptions{Delivery: sharingProductionDelivery{sender}})
			if err != nil {
				t.Fatal(err)
			}

			run := func(worker app.Worker) {
				t.Helper()
				if worked, err := worker.RunOnce(t.Context()); err != nil || !worked {
					t.Fatalf("worker: %v %v", worked, err)
				}
			}
			var update runtimecontract.UpdateID

			identity := func(user runtimecontract.UserID) interaction.Identity {
				update++
				return interaction.Identity{Update: update, User: user, Chat: -100, ChatKind: runtimecontract.ChatKindSupergroup}
			}
			dispatch := func(event interaction.Event) runtimecontract.EphemeralResponse {
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
					var err error

					result, err = handler.Handle(ctx, sharingProductionScope{tx, store}, event)
					return err
				}); err != nil {
					t.Fatal(err)
				}

				if len(result.ImmediateResponses) == 0 {
					return runtimecontract.EphemeralResponse{}
				}

				if len(result.ImmediateResponses) != 1 {
					t.Fatalf("expected one personal response, got %d", len(result.ImmediateResponses))
				}

				personal, ok := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

				if !ok {
					t.Fatal("interaction became public")
				}

				if _, err := sender.SendEphemeral(t.Context(), personal); err != nil {
					t.Fatal(err)
				}

				fields := <-requests

				if fields["ephemeral_message_parameters"] == "" && fields["receiver_user_id"] != "7" && fields["receiver_user_id"] != "8" {
					t.Fatal("browsing/guidance lost restricted transport")
				}

				return personal
			}
			command := func(user runtimecontract.UserID, name interaction.CommandName) runtimecontract.EphemeralResponse {
				return dispatch(interaction.Command{Identity: identity(user), Name: name, EphemeralMessageID: 1, SentAt: time.Now()})
			}
			button := func(view runtimecontract.EphemeralResponse, label string) string {
				t.Helper()
				for _, row := range view.Content.Buttons {
					for _, b := range row {
						if b.Text == label {
							return b.CallbackData
						}
					}
				}

				t.Fatalf("missing %q button in %+v", label, view.Content.Buttons)
				return ""
			}
			press := func(user runtimecontract.UserID, view runtimecontract.EphemeralResponse, data string) runtimecontract.EphemeralResponse {
				id := identity(user)
				body := view.Content
				body.Buttons = nil
				var callbacks []string

				for _, row := range view.Content.Buttons {
					for _, b := range row {
						callbacks = append(callbacks, b.CallbackData)
					}
				}
				return dispatch(interaction.Callback{Identity: id, QueryID: interaction.CallbackQueryID(fmt.Sprintf("inventory-%d", id.Update)), EphemeralMessageID: 900, Data: data, Source: &interaction.CallbackSource{Body: body, CallbackData: callbacks, EphemeralMessageID: 900}})
			}
			publicCount := func() int {
				t.Helper()
				var n int

				if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM outbound_deliveries WHERE kind='menu_share_public'`).Scan(&n); err != nil {
					t.Fatal(err)
				}

				return n
			}
			switchLocale := func(target string) {
				t.Helper()
				settings := command(7, "settings")
				for _, row := range settings.Content.Buttons {
					for _, b := range row {
						if strings.HasPrefix(b.CallbackData, "share:") {
							t.Fatal("settings became shareable")
						}
					}
				}

				label := "English"
				if target == "ru" {
					label = "Русский"
				}
				press(7, settings, button(settings, label))
				var saved string

				if err := db.QueryRowContext(t.Context(), `SELECT locale FROM group_settings WHERE chat_id=-100`).Scan(&saved); err != nil || saved != target {
					t.Fatalf("admin locale: %s %v", saved, err)
				}

				run(menusWorker)
				assertCommands(menus.commands)
				wantDescription := "Inspect your inventory"
				if target == "ru" {
					wantDescription = "Посмотреть инвентарь"
				}

				if menus.commands[1].Description != wantDescription {
					t.Fatal("group menu failed to follow locale")
				}
			}
			if locale == "ru" {
				switchLocale("ru")
			}
			shareLabel, equip, unequip, back := "Share", "Equip", "Unequip", "Back"
			if locale == "ru" {
				shareLabel, equip, unequip, back = "Поделиться", "Экипировать", "Снять", "Назад"
			}
			page := command(7, "inventory")
			button(page, shareLabel)
			// Select the starter Stick by its number (the catalog sorts it fourth).
			detail := press(7, page, button(page, "4"))
			equipped := press(7, detail, button(detail, equip))
			unequipped := press(7, equipped, button(equipped, unequip))
			navigated := press(7, unequipped, button(unequipped, back))
			button(navigated, shareLabel)

			if publicCount() != 0 {
				t.Fatal("ordinary equipment browsing queued a snapshot")
			}

			gameplay := func() string {
				t.Helper()
				var state string

				if err := db.QueryRowContext(t.Context(), `SELECT json_array((SELECT json_group_array(json_array(id,user_id,max_health,health,attack,defense,recover_at_ms,recovery_health,revision)) FROM characters),(SELECT json_group_array(json_array(character_id,revision)) FROM inventories),(SELECT json_group_array(json_array(character_id,item_id,quantity)) FROM inventory_items),(SELECT json_group_array(json_array(character_id,slot,item_id)) FROM inventory_equipment),(SELECT count(*) FROM inventory_operations))`).Scan(&state); err != nil {
					t.Fatal(err)
				}

				return state
			}
			before := gameplay()
			var copies []map[string]string

			share := func(view runtimecontract.EphemeralResponse, label string) {
				t.Helper()
				accepted := press(7, view, button(view, label))

				if accepted.Content.Text != "" {
					t.Fatalf("unexpected preparation guidance: %q", accepted.Content.Text)
				}

				run(worker)
				fields := <-requests

				if fields["text"] != view.Content.Text || fields["chat_id"] != "-100" || fields["ephemeral_message_parameters"] != "" || fields["reply_markup"] != "" || fields["reply_parameters"] != "" || !strings.HasSuffix(fields["method"], "/sendMessage") {
					t.Fatalf("invalid read-only public snapshot: %+v", fields)
				}

				copies = append(copies, fields)
				run(projector)
				if worked, err := worker.RunOnce(t.Context()); err != nil || worked {
					t.Fatalf("success queued feedback: %v %v", worked, err)
				}
			}
			cooldown := "You can share again in"
			if locale == "ru" {
				cooldown = "Можно поделиться снова через"
			}
			share(navigated, shareLabel)
			first := copies[0]["text"]
			blocked := press(7, navigated, button(navigated, shareLabel))

			if !strings.Contains(blocked.Content.Text, cooldown) || publicCount() != 1 {
				t.Fatal("double tap was not personally suppressed")
			}

			// Simulate an elapsed success interval in this isolated DB; production uses
			// time.Now. Exact 29.999s/30s boundaries use fake clocks in core tests.
			token := strings.TrimPrefix(button(navigated, shareLabel), "share:v1:")
			result, err := db.ExecContext(t.Context(), `UPDATE sharing_views SET last_success_at=?,revision=revision+1 WHERE token=?`, time.Now().Add(-31*time.Second).UnixMilli(), token)
			if err != nil {
				t.Fatal(err)
			}

			if n, err := result.RowsAffected(); err != nil || n != 1 {
				t.Fatalf("elapsed interval setup: %d %v", n, err)
			}

			share(navigated, shareLabel)

			if len(copies) != 2 || copies[0]["text"] != first || copies[1]["text"] != first || publicCount() != 2 {
				t.Fatal("intentional repost did not retain both identical copies")
			}

			// A newly opened detail freezes this locale, while all new guidance follows
			// the administrator's subsequent language selection.
			oldPage := command(7, "inventory")
			oldDetail := press(7, oldPage, button(oldPage, "4"))
			nextLocale := "ru"
			newLabel, newCooldown := "Поделиться", "Можно поделиться снова через"
			if locale == "ru" {
				nextLocale, newLabel, newCooldown = "en", "Share", "You can share again in"
			}
			switchLocale(nextLocale)
			fresh := command(7, "inventory")
			button(fresh, newLabel)
			member := command(8, "settings")

			if len(member.Content.Buttons) != 0 {
				t.Fatal("ordinary member gained settings authority")
			}

			// Even an owner's control cannot authorize another user's settings change.
			admin := command(7, "settings")
			target := "English"
			if locale == "ru" {
				target = "Русский"
			}
			press(8, admin, button(admin, target))
			var saved string

			if err := db.QueryRowContext(t.Context(), `SELECT locale FROM group_settings WHERE chat_id=-100`).Scan(&saved); err != nil || saved != nextLocale {
				t.Fatal("ordinary member changed group language")
			}

			share(oldDetail, shareLabel)
			denied := press(7, oldDetail, button(oldDetail, shareLabel))

			if !strings.Contains(denied.Content.Text, newCooldown) || publicCount() != 3 {
				t.Fatal("cooldown guidance ignored current language")
			}

			if copies[0]["text"] != first || copies[1]["text"] != first || copies[2]["text"] != oldDetail.Content.Text {
				t.Fatal("language switch changed a captured snapshot")
			}

			if !reflect.DeepEqual(options.Commands, []runtimecontract.CommandDeclaration{{Name: "character", Description: "Inspect your character", Ephemeral: true}, {Name: "inventory", Description: "Inspect your inventory", Ephemeral: true}, {Name: "settings", Description: "Inspect group language settings", Ephemeral: true}}) {
				t.Fatal("sharing changed global command declarations")
			}

			if gameplay() != before {
				t.Fatal("sharing/settings changed gameplay state")
			}
		})
	}
}
