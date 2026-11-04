package inventory

import (
	"context"
	"strings"
	"testing"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestDevelopmentInventorySelfGrantAndHealing(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	characters := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, characters, clock.Now)
	identity := interaction.Identity{Update: 100, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindGroup}
	handler := developmentHandler{service, clock.Now}
	var id charactercontract.ID
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 42)
		id = opened.Current.CharacterID
		return err
	})
	run := func(update runtimecontract.UpdateID, name interaction.CommandName, args string) string {
		t.Helper()
		var text string
		inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
			actor := identity
			actor.Update = update
			result, err := handler.Handle(ctx, inventoryScope{tx}, interaction.Command{Identity: actor, Name: name, Arguments: args, EphemeralMessageID: 1, SentAt: clock.Now()})
			if err != nil {
				return err
			}
			if len(result.ImmediateResponses) != 1 {
				t.Fatal("probe no personal response")
			}
			response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
			if response.TargetUserID != 42 || response.ReplaceCallbackMessage {
				t.Fatal("not self-only")
			}
			text = response.Content.Text
			return nil
		})
		return text
	}
	if text := run(100, "dev_inventory_grant", "coin 2"); !strings.Contains(text, "granted") {
		t.Fatal(text)
	}
	run(100, "dev_inventory_grant", "coin 2")
	if text := run(100, "dev_inventory_grant", "coin 3"); !strings.Contains(text, "conflict") {
		t.Fatal("changedinput reuse not rejected")
	}
	for _, args := range []string{"coin 1 target", "coin -1", "coin 0", "coin 1.5", "../bad 1", "coin 9223372036854775808"} {
		text := run(101, "dev_inventory_grant", args)
		if !strings.Contains(text, "Usage:") || strings.Contains(text, args) {
			t.Fatalf("unsafe arguments %q", text)
		}
	}
	if text := run(102, "dev_inventory_heal", "healing_potion"); !strings.Contains(text, "full health") {
		t.Fatal(text)
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := characters.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "test:devdamage", CharacterID: id, Kind: charactercontract.HealthDamage, Amount: 20})
		return err
	})
	if text := run(103, "dev_inventory_heal", "healing_potion"); !strings.Contains(text, "healed") {
		t.Fatal(text)
	}
	run(103, "dev_inventory_heal", "healing_potion")
	if text := run(103, "dev_inventory_heal", "coin"); !strings.Contains(text, "conflict") {
		t.Fatal("changed healing input accepted")
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		snapshot, _, err := readInventory(ctx, tx, catalog, id)
		if err != nil {
			return err
		}
		var coins, potions int64
		for _, item := range snapshot.Items {
			if item.ItemID == "coin" {
				coins = item.Quantity
			}
			if item.ItemID == "healing_potion" {
				potions = item.Quantity
			}
		}
		if coins != 12 || potions != 2 {
			t.Fatalf("duplicate mutation coins%d potions%d", coins, potions)
		}
		ch, _, err := characters.Lookup(ctx, tx, 42)
		if ch.Health != 90 {
			t.Fatalf("health%d", ch.Health)
		}
		return err
	})
	if text := run(104, "dev_inventory_heal", "coin"); !strings.Contains(text, "cannot be used") {
		t.Fatal("inert item gained healing effect")
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := characters.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "test:devdefeat", CharacterID: id, Kind: charactercontract.HealthDamage, Amount: 100})
		return err
	})
	if text := run(105, "dev_inventory_heal", "healing_potion"); !strings.Contains(text, "incapacitated") {
		t.Fatal("probe bypassed incapacitation")
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		quantity, err := ownedQuantity(ctx, tx, id, "healing_potion")
		if quantity != 2 {
			t.Fatal("rejected probe spent a potion")
		}
		return err
	})
	registry := interaction.NewRegistry()
	if err := RegisterDevelopment(registry, service, clock.Now); err != nil {
		t.Fatal(err)
	}
}
func TestDevelopmentInventoryAuthorization(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	handler := developmentHandler{service, clock.Now}
	for _, name := range []interaction.CommandName{"dev_inventory_grant", "dev_inventory_heal"} {
		command := interaction.Command{Identity: interaction.Identity{Update: 1, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindGroup}, Name: name, Arguments: "coin 2", SentAt: clock.Now()}
		inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
			result, err := handler.Handle(ctx, inventoryScope{tx}, command)
			if len(result.ImmediateResponses) != 0 {
				t.Fatal("ordinary probe acted")
			}
			return err
		})
	}
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM characters`).Scan(&count); err != nil || count != 0 {
		t.Fatal("unauthorized create")
	}
}
