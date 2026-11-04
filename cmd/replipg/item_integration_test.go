package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestItemProductionBinaryReadOnlyAndOwnerAuthorization(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	binary := buildInventoryBinary(t, ctx, false)
	dir := t.TempDir()
	h := startInventoryBinary(t, ctx, binary, dir, 4, true, map[string]string{envDevInventory: "true", envOwnerTelegramUserID: "7"})
	db := inventoryBinaryDB(t, dir)
	defer func() { _ = db.Close() }()
	assertEmpty := func() {
		t.Helper()
		for _, table := range []string{"characters", "inventories", "inventory_items", "inventory_equipment", "inventory_controls", "inventory_operations", "character_operations", "group_settings", "settings_controls", "sharing_views", "sharing_actions"} {
			var count int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+table).Scan(&count); err != nil {
				t.Fatal(err)
			}

			if count != 0 {
				t.Fatalf("probe wrote %s: %d", table, count)
			}
		}
	}

	assertSilent := func() {
		t.Helper()
		select {
		case reply := <-h.replies:
			t.Fatalf("unauthorized reply: %q", reply.text)
		default:
		}
	}

	for _, check := range []struct{ command, want string }{{"/dev_item stick", "Attack bonus: +1"}, {"/dev_item cloth_armor", "Defense bonus: +1"}, {"/dev_item healing_potion", "10"}, {"/dev_item coin", "Coin"}, {"/dev_item", "Usage:"}, {"/dev_item coin extra", "Usage:"}, {"/dev_item ../bad", "Usage:"}, {"/dev_item unknown", "Unknown item"}} {
		h.submit(t, 7, -100, "owner", check.command, false)
		reply := h.reply(t, 7, -100)
		if !strings.Contains(reply.text, check.want) || len(reply.buttons) != 0 {
			t.Fatalf("invalid item reply: %+v", reply)
		}

		assertEmpty()
	}

	// An owner-looking username cannot authorize another numeric actor.
	for _, command := range []string{"/dev_item coin", "/dev_inventory_grant coin 1", "/dev_inventory_heal healing_potion"} {
		h.submit(t, 8, -100, "owner", command, false)
		assertSilent()
		assertEmpty()
	}

	h.submit(t, 7, -100, "owner", "/dev_item coin", false, true)
	assertSilent()
	assertEmpty()
	last := h.id
	h.stop(t)
	h = startInventoryBinary(t, ctx, binary, dir, 4, false, map[string]string{envDevInventory: "true", envOwnerTelegramUserID: "8"})
	h.id = last
	// The fixture grants user 7 administrator status; the configured owner is 8.
	for _, command := range []string{"/dev_item coin", "/dev_inventory_grant coin 1", "/dev_inventory_heal healing_potion"} {
		h.submit(t, 7, -100, "owner", command, false)
		assertSilent()
		assertEmpty()
	}

	last = h.id
	h.stop(t)
	h = startInventoryBinary(t, ctx, binary, dir, 4, false, map[string]string{envDevInventory: "false"})
	h.id = last
	for _, command := range []string{"/dev_item coin", "/dev_inventory_grant coin 1", "/dev_inventory_heal healing_potion"} {
		h.submit(t, 7, -100, "owner", command, false)
		assertSilent()
		assertEmpty()
	}

	h.submit(t, 8, -100, "ordinary_player", "/inventory", false)
	if reply := h.reply(t, 8, -100); !strings.Contains(reply.text, "Inventory • ordinary_player") {
		t.Fatal("nonowner player inventory inaccessible")
	}

	h.stop(t)
}
