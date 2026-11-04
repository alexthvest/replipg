package inventory

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestEquipmentConcurrentControlsAcrossGroups(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	handler := inventoryHandler{service: service, now: clock.Now, presentation: englishPresentation(t)}
	var before contract.Snapshot
	type pending struct {
		identity interaction.Identity
		token    string
	}

	controls := make([]pending, 16)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}

		before = opened.Current
		for i := range controls {
			item, slot := contract.ItemID("stick"), contract.SlotWeapon
			if i%2 != 0 {
				item, slot = "cloth_armor", contract.SlotArmor
			}
			identity := interaction.Identity{Update: runtimecontract.UpdateID(i + 1), User: 42, Chat: runtimecontract.ChatID(-100 - i%2), ChatKind: runtimecontract.ChatKindGroup}
			token, err := createControl(ctx, tx, clock.Now(), inventoryControl{UserID: 42, ChatID: identity.Chat, CharacterID: before.CharacterID, Action: "equip", ItemID: item, Slot: slot, ExpectedRevision: before.Revision})
			if err != nil {
				return err
			}

			controls[i] = pending{identity, token}
		}
		return nil
	})
	type outcome struct {
		index   int
		content string
		err     error
	}

	results := make(chan outcome, len(controls))
	start := make(chan struct{})
	var wg sync.WaitGroup

	for i, control := range controls {
		wg.Go(func() {
			<-start
			var content string

			err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				result, err := handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: control.identity, QueryID: interaction.CallbackQueryID(fmt.Sprintf("concurrent-%d", i)), Data: control.token})
				if err != nil {
					return err
				}

				if len(result.ImmediateResponses) != 1 {
					return fmt.Errorf("control %d has no personal response", i)
				}

				response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
				if response.TargetUserID != 42 || response.Destination.ChatID != control.identity.Chat {
					return fmt.Errorf("control %d response binding", i)
				}

				content = response.Content.Text
				return nil
			})
			results <- outcome{i, content, err}
		})
	}

	close(start)
	wg.Wait()
	close(results)
	accepted, rejected, winner := 0, 0, -1
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}

		if strings.Contains(result.content, "Inventory changed") {
			rejected++
		} else if strings.Contains(result.content, "\nEquipped") {
			accepted++
			winner = result.index
		} else {
			t.Fatalf("unexpected equipment result %q", result.content)
		}
	}

	if accepted != 1 || rejected != len(controls)-1 {
		t.Fatalf("accepted=%d stale=%d", accepted, rejected)
	}

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		after, _, err := readInventory(ctx, tx, catalog, before.CharacterID)
		if err != nil {
			return err
		}

		if after.Revision != before.Revision+1 || !reflect.DeepEqual(after.Items, before.Items) {
			t.Fatal("concurrent controls changed ownership or advanced more than once")
		}

		if winner%2 == 0 && (after.Weapon != "stick" || after.Armor != "" || after.Modifiers.Attack != 1 || after.Modifiers.Defense != 0) || winner%2 != 0 && (after.Weapon != "" || after.Armor != "cloth_armor" || after.Modifiers.Attack != 0 || after.Modifiers.Defense != 1) {
			t.Fatalf("winner and persisted assignment disagree: %+v", after)
		}

		for i := range 3 {
			result, err := handler.Handle(ctx, inventoryScope{tx}, interaction.Callback{EphemeralMessageID: 900, Identity: controls[winner].identity, QueryID: interaction.CallbackQueryID(fmt.Sprintf("replay-%d", i)), Data: controls[winner].token})
			if err != nil {
				return err
			}

			if !strings.Contains(result.ImmediateResponses[0].(runtimecontract.EphemeralResponse).Content.Text, "\nEquipped") {
				t.Fatal("accepted control replay rejected")
			}
		}

		replayed, _, err := readInventory(ctx, tx, catalog, before.CharacterID)
		if err != nil {
			return err
		}

		if !reflect.DeepEqual(after, replayed) {
			t.Fatal("accepted replay changed inventory")
		}

		var receipts int

		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_operations WHERE kind='equipment'`).Scan(&receipts); err != nil {
			return err
		}

		if receipts != 1 {
			t.Fatalf("equipment receipts=%d", receipts)
		}

		return nil
	})
}
