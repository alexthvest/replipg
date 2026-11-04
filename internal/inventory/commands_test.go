package inventory

import (
	"context"
	"strings"
	"testing"
	"time"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type inventoryScope struct{ tx runtimecontract.DBTX }

func (s inventoryScope) DBTX() runtimecontract.DBTX { return s.tx }

func (inventoryScope) EnqueueDelivery(context.Context, runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	panic("unexpected delivery")
}

func (inventoryScope) Schedule(context.Context, runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	panic("unexpected job")
}

func TestInventoryCommandAuthorization(t *testing.T) {
	for _, variant := range []string{"valid", "ordinary", "bot", "anonymous", "expired", "private", "arguments"} {
		t.Run(variant, func(t *testing.T) {
			db, catalog, clock, _ := inventoryFixture(t)
			service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error { return service.Prepare(ctx, tx) })
			command := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -1, User: 42, ChatKind: runtimecontract.ChatKindGroup, ActorUsername: "alice\n"}, Name: "inventory", EphemeralMessageID: 1, SentAt: clock.Now()}
			switch variant {
			case "ordinary":
				command.EphemeralMessageID = 0
			case "bot":
				command.ActorIsBot = true
			case "anonymous":
				command.SenderIsChat = true
			case "expired":
				command.SentAt = clock.Now().Add(-15 * time.Second)
			case "private":
				command.Chat = 1
			case "arguments":
				command.Arguments = "sensitive target"
			}

			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				result, err := (inventoryHandler{service: service, now: clock.Now, presentation: englishPresentation(t)}).Handle(ctx, inventoryScope{tx}, command)
				if err != nil {
					return err
				}

				want := variant == "valid" || variant == "arguments"

				if (len(result.ImmediateResponses) == 1) != want {
					t.Fatalf("responses %+v", result)
				}

				if want {
					response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

					if response.TargetUserID != 42 || response.ReplaceCallbackMessage || response.EditMessageID != 0 || response.Destination.ChatID != -1 {
						t.Fatal("personal destination")
					}

					if err := runtimecontract.ValidateResponseAt(response, clock.Now()); err != nil {
						return err
					}

					if variant == "valid" && !strings.Contains(response.Content.Text, "Inventory • alice") {
						t.Fatal("missing summary")
					}

					if strings.Contains(response.Content.Text, "sensitive target") {
						t.Fatal("argument leaked")
					}
				}

				var count int

				if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM characters`).Scan(&count); err != nil {
					return err
				}

				expected := 0
				if variant == "valid" {
					expected = 1
				}

				if count != expected {
					t.Fatalf("unauthorized initialization count%d", count)
				}

				return nil
			})
		})
	}
}

func TestInventoryNavigationIsPersonalAndRefreshed(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	handler := inventoryHandler{service: service, now: clock.Now, presentation: englishPresentation(t)}
	var data string

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		event := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -1, User: 42, ChatKind: runtimecontract.ChatKindGroup}, Name: "inventory", EphemeralMessageID: 1, SentAt: clock.Now()}
		result, err := handler.Handle(ctx, inventoryScope{tx}, event)
		if err != nil {
			return err
		}

		data = result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Buttons[0][1].CallbackData
		return nil
	})
	for _, variant := range []string{"valid", "foreign", "other_group", "unknown", "ordinary", "expired"} {
		t.Run(variant, func(t *testing.T) {
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				event := interaction.Callback{EphemeralMessageID: 900, Identity: interaction.Identity{Update: 2, Chat: -1, User: 42, ChatKind: runtimecontract.ChatKindGroup}, QueryID: "fresh-query", Data: data}
				switch variant {
				case "foreign":
					event.User = 43
				case "other_group":
					event.Chat = -2
				case "unknown":
					event.Data = "inv:unknown"
				case "ordinary":
					event.EphemeralMessageID = 0
				case "expired":
					clock.Advance(5 * time.Minute)
				}

				result, err := handler.Handle(ctx, inventoryScope{tx}, event)
				if err != nil {
					return err
				}

				if variant == "foreign" || variant == "other_group" || variant == "unknown" || variant == "ordinary" {
					if len(result.ImmediateResponses) != 0 {
						t.Fatal("foreign details disclosed")
					}

					return nil
				}

				if len(result.ImmediateResponses) != 1 {
					t.Fatal("no refreshed response")
				}

				response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
				auth := response.Authorization.(runtimecontract.CallbackAuthorization)

				if auth.CallbackQueryID != "fresh-query" || !auth.OriginatedFromEphemeral || response.ReplaceCallbackMessage || response.EditMessageID != 900 {
					t.Fatal("callback authorization")
				}

				if variant == "valid" && !strings.Contains(response.Content.Text, "Coin • you") {
					t.Fatal("detail mapping")
				}

				if variant == "expired" && !strings.Contains(response.Content.Text, "/inventory") {
					t.Fatal("no reopen guidance")
				}

				return runtimecontract.ValidateResponseAt(response, clock.Now())
			})
		})
	}
}

func TestInventoryHandlerNavigationAndDeadlines(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	handler := inventoryHandler{service: service, now: clock.Now, presentation: englishPresentation(t)}
	var detailToken string
	var characterID charactercontract.ID

	identity := interaction.Identity{Update: 1, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindGroup}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		sent := clock.Now().Add(-10 * time.Second)
		result, err := handler.Handle(ctx, inventoryScope{tx}, interaction.Command{Identity: identity, Name: "inventory", EphemeralMessageID: 1, SentAt: sent})
		if err != nil {
			return err
		}

		response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

		if !response.Authorization.(runtimecontract.IncomingEphemeralAuthorization).ValidUntil.Equal(sent.Add(15 * time.Second)) {
			t.Fatal("command extended original timestamp")
		}

		detailToken = response.Content.Buttons[0][1].CallbackData
		control, err := resolveControl(ctx, tx, clock.Now(), 42, -1, detailToken)
		characterID = control.CharacterID
		return err
	})
	clock.Advance(4*time.Minute + 55*time.Second)
	var backToken string

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: identity, QueryID: "near-expiry", Data: detailToken})
		if err != nil {
			return err
		}

		response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
		auth := response.Authorization.(runtimecontract.CallbackAuthorization)

		if !auth.ValidUntil.Equal(clock.Now().Add(5*time.Second)) || !auth.OriginatedFromEphemeral || response.EditMessageID != 900 {
			t.Fatal("callback deadline/origin not derived from control/event")
		}

		backToken = response.Content.Buttons[len(response.Content.Buttons)-1][0].CallbackData
		return nil
	})
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: identity, QueryID: "back", Data: backToken})
		if err != nil {
			return err
		}

		if !strings.HasPrefix(result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Text, "Inventory • you") {
			t.Fatal("Back did not refresh page")
		}

		// A current read control clamps an out-of-range page after content changes.
		page, err := createControl(ctx, tx, clock.Now(), inventoryControl{UserID: 42, ChatID: -1, CharacterID: characterID, Action: "page", Page: 999, ExpectedRevision: 2})
		if err != nil {
			return err
		}

		if err := decreaseQuantity(ctx, tx, characterID, "coin", 10); err != nil {
			return err
		}

		result, err = handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: identity, QueryID: "clamp", Data: page})
		if err != nil {
			return err
		}

		content := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content

		if !strings.Contains(content.Text, "1.") || strings.Contains(content.Text, "Coin (") {
			t.Fatal("page did not refresh/clamp current ownership")
		}

		result, err = handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: identity, QueryID: "stale", Data: detailToken})
		if err != nil {
			return err
		}

		if !strings.Contains(result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Text, "Inventory changed. Open /inventory") {
			t.Fatal("removed item disclosed stale detail")
		}

		return nil
	})
	registry := interaction.NewRegistry()
	if err := Register(registry, service, clock.Now, englishPresentation(t)); err != nil {
		t.Fatal(err)
	}

	registry.Seal()
	for _, event := range []interaction.Event{interaction.Command{Identity: identity, Name: "inventory"}, interaction.Callback{EphemeralMessageID: 900, Identity: identity, Data: detailToken}} {
		if _, ok := registry.Resolve(event); !ok {
			t.Fatal("production route missing")
		}
	}
}

func TestInventoryEquipmentControlsReplayAndStaleness(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	handler := inventoryHandler{service: service, now: clock.Now, presentation: englishPresentation(t)}
	identity := interaction.Identity{Update: 1, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindGroup}
	var equip, stale, unequip string

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}

		token, err := createControl(ctx, tx, clock.Now(), inventoryControl{UserID: 42, ChatID: -1, CharacterID: opened.Current.CharacterID, Action: "detail", ItemID: "stick", ExpectedRevision: opened.Current.Revision})
		if err != nil {
			return err
		}

		result, err := handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: identity, QueryID: "details", Data: token})
		if err != nil {
			return err
		}

		content := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content

		if content.Buttons[0][0].Text != "Equip" {
			t.Fatalf("no equipment action %+v", content.Buttons)
		}

		equip = content.Buttons[0][0].CallbackData
		result, err = handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: identity, QueryID: "other-details", Data: token})
		if err != nil {
			return err
		}

		stale = result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Buttons[0][0].CallbackData
		for _, foreign := range []interaction.Identity{
			{Update: 2, User: 43, Chat: -1, ChatKind: runtimecontract.ChatKindGroup},
			{Update: 2, User: 42, Chat: -2, ChatKind: runtimecontract.ChatKindGroup},
		} {
			response, err := handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: foreign, QueryID: "foreign-mutation", Data: equip})
			if err != nil {
				return err
			}

			if len(response.ImmediateResponses) != 0 {
				t.Fatal("foreign mutation control disclosed state")
			}
		}
		return nil
	})
	call := func(data, query string) runtimecontract.MessageContent {
		t.Helper()
		var content runtimecontract.MessageContent

		inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
			result, err := handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: identity, QueryID: interaction.CallbackQueryID(query), Data: data})
			if err != nil {
				return err
			}

			content = result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content
			return nil
		})
		return content
	}
	content := call(equip, "equip")

	if !strings.Contains(content.Text, "Equipped") || content.Buttons[0][0].Text != "Unequip" {
		t.Fatalf("equip response %+v", content)
	}

	unequip = content.Buttons[0][0].CallbackData
	content = call(stale, "stale")

	if !strings.Contains(content.Text, "Inventory changed") {
		t.Fatalf("unused stale control accepted %s", content.Text)
	}

	content = call(unequip, "unequip")

	if !strings.Contains(content.Text, "Unequipped") {
		t.Fatal("unequip")
	}

	content = call(equip, "different-delivery-replay")

	if !strings.Contains(content.Text, "Unequipped") {
		t.Fatal("accepted control reapplied old equipment")
	}

	var count int

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM inventory_operations WHERE kind='equipment'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("duplicate/stale receipts %d %v", count, err)
	}

	clock.Advance(5 * time.Minute)
	content = call(equip, "expired-accepted-control")

	if !strings.Contains(content.Text, "expired") {
		t.Fatal("accepted replay bypassed control expiry")
	}

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM inventory_operations WHERE kind='equipment'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("expired replay changed receipts")
	}
}
