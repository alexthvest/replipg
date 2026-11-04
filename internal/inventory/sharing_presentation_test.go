package inventory

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	sharingcontract "github.com/alexthvest/replipg/internal/sharing/contract"
)

type capturingInventoryDecorator struct {
	views []sharingcontract.View
	tx    runtimecontract.DBTX
	err   error
}

func (d *capturingInventoryDecorator) Decorate(_ context.Context, tx runtimecontract.DBTX, view sharingcontract.View) (runtimecontract.MessageContent, error) {
	d.views = append(d.views, view)
	d.tx = tx
	body := view.Body
	body.Buttons = append(append([][]runtimecontract.InlineButton(nil), body.Buttons...), []runtimecontract.InlineButton{{Text: "Share", CallbackData: "share:v1:0123456789abcdef0123456789abcdef"}})
	return body, d.err
}

func inventorySharingPresentation(t *testing.T, decorator sharingcontract.Decorator, locale localization.Locale) Presentation {
	t.Helper()
	catalog, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	return Presentation{Catalog: catalog, Sharing: decorator, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return locale, nil
	}}
}

func TestInventorySharingPresentation(t *testing.T) {
	for _, locale := range []localization.Locale{localization.English, localization.Russian} {
		t.Run(string(locale), func(t *testing.T) {
			db, catalog, clock, _ := inventoryFixture(t)
			service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
			inventoryTx(t, db, service.Prepare)
			decorator := &capturingInventoryDecorator{}
			p := inventorySharingPresentation(t, decorator, locale)
			resolveCalls := 0
			p.Resolve = func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
				resolveCalls++
				return locale, nil
			}
			nowCalls := 0
			now := func() time.Time {
				nowCalls++
				return clock.Now().Add(time.Duration(nowCalls)*time.Millisecond + 123*time.Microsecond)
			}
			registry := interaction.NewRegistry()
			if err := Register(registry, service, now, p); err != nil {
				t.Fatal(err)
			}

			registry.Seal()
			identity := interaction.Identity{Update: 1, Chat: -100, User: 42, ChatKind: runtimecontract.ChatKindSupergroup, ActorUsername: "alice"}
			call := func(event interaction.Event, kind sharingcontract.ViewKind, editID int64) runtimecontract.MessageContent {
				t.Helper()
				before := len(decorator.views)
				priorNowCalls := nowCalls
				var content runtimecontract.MessageContent

				inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
					h, ok := registry.Resolve(event)

					if !ok {
						t.Fatal("missing inventory route")
					}

					result, err := h.Handle(ctx, inventoryScope{tx}, event)
					if err != nil {
						return err
					}

					if len(decorator.views) != before+1 {
						t.Fatalf("decoration calls = %d, want %d", len(decorator.views), before+1)
					}

					view := decorator.views[before]

					if len(result.ImmediateResponses) != 1 {
						t.Fatal("inventory must remain personal")
					}

					response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

					if view.Owner != identity.User || view.Destination != response.Destination || view.Kind != kind || view.Locale != locale || view.EditMessageID != editID || decorator.tx != tx || response.TargetUserID != identity.User || int64(response.EditMessageID) != editID {
						t.Fatalf("incorrect view/response metadata: %+v %+v", view, response)
					}

					if nowCalls != priorNowCalls+1 || resolveCalls != before+1 {
						t.Fatal("render time/locale must each resolve once")
					}

					deadline := time.UnixMilli(clock.Now().Add(time.Duration(nowCalls)*time.Millisecond + 123*time.Microsecond + controlLifetime).UnixMilli()).UTC()

					if !view.UsableUntil.Equal(deadline) {
						t.Fatalf("deadline = %v, want %v", view.UsableUntil, deadline)
					}

					content = response.Content

					if view.Body.Text != content.Text || !reflect.DeepEqual(view.Body.Entities, content.Entities) || !reflect.DeepEqual(view.Body.Buttons, content.Buttons[:len(content.Buttons)-1]) {
						t.Fatal("body or existing controls changed")
					}

					for _, row := range view.Body.Buttons {
						for _, button := range row {
							control, err := resolveControl(ctx, tx, clock.Now(), identity.User, identity.Chat, button.CallbackData)
							if err != nil {
								return err
							}

							if !control.ExpiresAt.Equal(view.UsableUntil) {
								t.Fatal("Share expiry differs from rendered controls")
							}
						}
					}
					return runtimecontract.ValidateResponseAt(response, clock.Now())
				})
				return content
			}
			command := interaction.Command{Identity: identity, Name: "inventory", EphemeralMessageID: 23, SentAt: clock.Now()}
			content := call(command, sharingcontract.InventoryPage, 0)
			want := "Inventory • alice"
			if locale == localization.Russian {
				want = "Инвентарь • alice"
			}

			if !strings.Contains(content.Text, want) {
				t.Fatal("final body has wrong locale")
			}

			var detail string

			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				for _, row := range decorator.views[0].Body.Buttons {
					for _, button := range row {
						control, err := resolveControl(ctx, tx, clock.Now(), identity.User, identity.Chat, button.CallbackData)
						if err != nil {
							return err
						}

						if control.ItemID == "stick" {
							detail = button.CallbackData
						}
					}
				}
				return nil
			})

			if detail == "" {
				t.Fatal("missing stick control")
			}

			callback := func(data string, kind sharingcontract.ViewKind) runtimecontract.MessageContent {
				return call(interaction.Callback{Identity: identity, QueryID: "query", EphemeralMessageID: 900, Data: data}, kind, 900)
			}
			clock.Advance(4*time.Minute + 55*time.Second)
			content = callback(detail, sharingcontract.ItemDetails)
			// Each newly rendered view inherits its new controls' deadline, even
			// when the input control had only a few seconds left.
			content = callback(content.Buttons[0][0].CallbackData, sharingcontract.ItemDetails) // equip
			content = callback(content.Buttons[0][0].CallbackData, sharingcontract.ItemDetails) // unequip
			back := content.Buttons[len(content.Buttons)-2][0].CallbackData
			callback(back, sharingcontract.InventoryPage)
		})
	}
}

func TestInventorySharingExcludesGuidanceAndDevelopment(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	inventoryTx(t, db, service.Prepare)
	decorator := &capturingInventoryDecorator{}
	p := inventorySharingPresentation(t, decorator, localization.English)
	h := inventoryHandler{service: service, now: clock.Now, presentation: p}
	identity := interaction.Identity{Update: 1, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindGroup}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		opened, err := service.Open(ctx, tx, identity.User)
		if err != nil {
			return err
		}

		stale, err := createControl(ctx, tx, clock.Now(), inventoryControl{UserID: identity.User, ChatID: identity.Chat, CharacterID: opened.Current.CharacterID, Action: "detail", ItemID: "coin", ExpectedRevision: opened.Current.Revision})
		if err != nil {
			return err
		}

		if err := decreaseQuantity(ctx, tx, opened.Current.CharacterID, "coin", 10); err != nil {
			return err
		}

		for _, event := range []interaction.Event{
			interaction.Command{Identity: identity, Name: "inventory", Arguments: "unexpected", EphemeralMessageID: 1, SentAt: clock.Now()},
			interaction.Callback{Identity: identity, QueryID: "stale", EphemeralMessageID: 900, Data: stale},
		} {
			result, err := h.Handle(ctx, inventoryScope{tx}, event)
			if err != nil {
				return err
			}

			if len(result.ImmediateResponses) != 1 || len(result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Buttons) != 0 {
				t.Fatal("guidance changed")
			}
		}

		clock.Advance(controlLifetime)
		if _, err := h.Handle(ctx, inventoryScope{tx}, interaction.Callback{Identity: identity, QueryID: "expired", EphemeralMessageID: 900, Data: stale}); err != nil {
			return err
		}

		dev := developmentHandler{service: service, now: clock.Now, presentation: p}
		for _, command := range []interaction.Command{
			{Identity: identity, Name: "dev_inventory_grant", Arguments: "coin 1", EphemeralMessageID: 1, SentAt: clock.Now()},
			{Identity: identity, Name: "dev_inventory_heal", Arguments: "healing_potion", EphemeralMessageID: 1, SentAt: clock.Now()},
		} {
			if _, err := dev.Handle(ctx, inventoryScope{tx}, command); err != nil {
				return err
			}
		}

		if len(decorator.views) != 0 {
			t.Fatal("unsupported content decorated")
		}

		return nil
	})
}

func TestInventorySharingFailureRollsBack(t *testing.T) {
	for _, mutation := range []bool{false, true} {
		t.Run(map[bool]string{false: "open", true: "equip"}[mutation], func(t *testing.T) {
			db, catalog, clock, _ := inventoryFixture(t)
			service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
			inventoryTx(t, db, service.Prepare)
			failure := errors.New("decoration storage failure")
			decorator := &capturingInventoryDecorator{err: failure}
			p := inventorySharingPresentation(t, decorator, localization.English)
			h := inventoryHandler{service: service, now: clock.Now, presentation: p}
			identity := interaction.Identity{Update: 1, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindGroup}
			var event interaction.Event = interaction.Command{Identity: identity, Name: "inventory", EphemeralMessageID: 1, SentAt: clock.Now()}
			var controlsBefore int

			if mutation {
				inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
					opened, err := service.Open(ctx, tx, identity.User)
					if err != nil {
						return err
					}

					data, err := createControl(ctx, tx, clock.Now(), inventoryControl{UserID: identity.User, ChatID: identity.Chat, CharacterID: opened.Current.CharacterID, Action: "equip", ItemID: "stick", Slot: "weapon", ExpectedRevision: opened.Current.Revision})
					event = interaction.Callback{Identity: identity, QueryID: "equip", EphemeralMessageID: 900, Data: data}
					return err
				})
				controlsBefore = 1
			}
			err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				result, err := h.Handle(ctx, inventoryScope{tx}, event)

				if len(result.ImmediateResponses) != 0 {
					t.Fatal("failed decoration returned a response")
				}

				return err
			})
			if !errors.Is(err, failure) {
				t.Fatalf("decoration failure not propagated: %v", err)
			}

			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				for _, table := range []string{"inventory_controls", "inventory_equipment", "inventory_operations", "characters"} {
					var count int

					if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
						return err
					}

					want := 0
					if table == "inventory_controls" {
						want = controlsBefore
					}
					if mutation && (table == "characters" || table == "inventory_operations") {
						want = 1
					}

					if count != want {
						t.Fatalf("%s count = %d, want %d after rollback", table, count, want)
					}
				}
				return nil
			})
		})
	}
}
