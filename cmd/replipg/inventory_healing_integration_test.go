package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestInventoryProductionBinaryHealingProbeLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	binary := buildInventoryBinary(t, ctx, false)
	dir := t.TempDir()
	enabled := map[string]string{envDevCharacter: "true", envDevInventory: "true"}
	h := startInventoryBinary(t, ctx, binary, dir, 4, true, enabled)
	db := inventoryBinaryDB(t, dir)
	defer func() { _ = db.Close() }()

	call := func(data string, callback bool) inventoryBinaryReply {
		t.Helper()
		h.submit(t, 7, -100, "healing_owner", data, callback)
		return h.reply(t, 7, -100)
	}
	assertText := func(reply inventoryBinaryReply, text string) {
		t.Helper()

		if !strings.Contains(reply.text, text) {
			t.Fatalf("missing %q in %q", text, reply.text)
		}
	}
	assertHealthQuantity := func(user, health, quantity int64) {
		t.Helper()
		var gotHealth, gotQuantity int64

		if err := db.QueryRowContext(ctx, `SELECT health,COALESCE((SELECT quantity FROM inventory_items WHERE character_id=characters.id AND item_id='healing_potion'),0) FROM characters WHERE user_id=?`, user).Scan(&gotHealth, &gotQuantity); err != nil {
			t.Fatal(err)
		}

		if gotHealth != health || gotQuantity != quantity {
			t.Fatalf("actor %d: health/quantity=%d/%d, want %d/%d", user, gotHealth, gotQuantity, health, quantity)
		}
	}
	// Include every actor's domain rows and both receipt namespaces, while
	// deliberately excluding runtime intake bookkeeping and expiring view controls.
	domainState := func() string {
		t.Helper()
		var state string

		err := db.QueryRowContext(ctx, `SELECT json_array(
			(SELECT json_group_array(json_array(id,user_id,max_health,health,attack,defense,recover_at_ms,recovery_health,revision)) FROM (SELECT * FROM characters ORDER BY id)),
			(SELECT json_group_array(json_array(character_id,starter_granted,revision)) FROM (SELECT * FROM inventories ORDER BY character_id)),
			(SELECT json_group_array(json_array(character_id,item_id,quantity)) FROM (SELECT * FROM inventory_items ORDER BY character_id,item_id)),
			(SELECT count(*) FROM inventory_operations),
			(SELECT count(*) FROM character_operations),
			(SELECT count(*) FROM inventory_equipment))`).Scan(&state)
		if err != nil {
			t.Fatal(err)
		}

		return state
	}
	assertUnchanged := func(before string) {
		t.Helper()

		if domainState() != before {
			t.Fatal("rejected or disabled command changed persistent domain state")
		}
	}
	assertSilent := func() {
		t.Helper()
		select {
		case reply := <-h.replies:
			t.Fatalf("unauthorized or disabled command replied: %q", reply.text)
		default:
		}
	}

	first := call("/inventory", false)
	assertText(first, "Healing potion (3)")
	potion := call(first.button(t, "3"), true)
	assertText(potion, "Usable during battle")

	if len(potion.buttons) != 2 || len(potion.buttons[0]) != 1 || potion.buttons[0][0].Text != "Back" || len(potion.buttons[1]) != 1 || potion.buttons[1][0].Text != "Share" {
		t.Fatal("production inventory exposed a standalone healing action")
	}

	h.submit(t, 8, -100, "other_owner", "/inventory", false)
	h.reply(t, 8, -100)
	assertHealthQuantity(7, 100, 3)
	assertHealthQuantity(8, 100, 3)

	before := domainState()
	// Typed commands cannot borrow the development probes' ephemeral authority.
	h.submit(t, 7, -100, "healing_owner", "/dev_inventory_grant healing_potion 10", false, true)
	h.submit(t, 7, -100, "healing_owner", "/dev_inventory_heal healing_potion", false, true)
	assertSilent()
	assertUnchanged(before)
	assertText(call("/dev_inventory_grant healing_potion 2 8", false), "Usage:")
	assertText(call("/dev_inventory_heal healing_potion 8", false), "Usage:")
	assertUnchanged(before)
	assertText(call("/dev_inventory_heal healing_potion", false), "full health")
	assertUnchanged(before)
	assertText(call("/dev_character_damage 20", false), "80/100")
	assertText(call("/dev_inventory_heal healing_potion", false), "Character healed")
	assertHealthQuantity(7, 90, 2)
	assertText(call("/character", false), "90/100")
	assertText(call("/inventory", false), "Healing potion (2)")
	assertText(call("/dev_inventory_heal healing_potion", false), "Character healed")
	assertHealthQuantity(7, 100, 1)
	before = domainState()
	assertText(call("/dev_inventory_heal healing_potion", false), "full health")
	assertUnchanged(before)
	assertText(call("/dev_character_defeat", false), "incapacitated")
	// Set only this isolated database's saved domain deadline. No transport
	// timestamp or production clock is overridden, and no wall-clock sleep is
	// needed to keep the rejection deterministic on slow test machines.
	deadline := time.Now().Add(time.Hour).UnixMilli()
	if _, err := db.ExecContext(ctx, `UPDATE characters SET recover_at_ms=? WHERE user_id=7`, deadline); err != nil {
		t.Fatal(err)
	}

	before = domainState()
	assertText(call("/dev_inventory_heal healing_potion", false), "incapacitated")
	assertUnchanged(before)
	assertHealthQuantity(7, 0, 1)
	h.stop(t)
	previousID := h.id
	h = startInventoryBinary(t, ctx, binary, dir, 4, false, enabled)
	h.id = previousID
	assertUnchanged(before)
	assertText(call("/dev_inventory_heal healing_potion", false), "incapacitated")
	assertUnchanged(before)
	var savedDeadline int64

	if err := db.QueryRowContext(ctx, `SELECT recover_at_ms FROM characters WHERE user_id=7`).Scan(&savedDeadline); err != nil || savedDeadline != deadline {
		t.Fatal("restart changed saved recovery deadline")
	}

	if _, err := db.ExecContext(ctx, `UPDATE characters SET recover_at_ms=? WHERE user_id=7`, time.Now().Add(-time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}

	// The same production action observes due recovery (25 HP), then heals 10
	// and removes the final owned copy atomically.
	assertText(call("/dev_inventory_heal healing_potion", false), "Character healed")
	assertHealthQuantity(7, 35, 0)
	assertText(call("/character", false), "35/100")

	if strings.Contains(call("/inventory", false).text, "Healing potion") {
		t.Fatal("exhausted healing item remained in inventory")
	}

	before = domainState()
	assertText(call("/dev_inventory_heal healing_potion", false), "do not own")
	assertUnchanged(before)
	assertText(call("/dev_inventory_grant healing_potion 2", false), "reward granted")
	assertHealthQuantity(7, 35, 2)
	assertHealthQuantity(8, 100, 3)
	before = domainState()
	h.stop(t)
	previousID = h.id
	h = startInventoryBinary(t, ctx, binary, dir, 4, false, map[string]string{envDevCharacter: "false", envDevInventory: "false"})
	h.id = previousID
	assertUnchanged(before)
	// Startup has asserted the complete normal menu. Both stale ephemeral
	// commands and manually typed variants must now lack registered handlers.
	for _, command := range []string{"/dev_inventory_grant healing_potion 20", "/dev_inventory_heal healing_potion", "/dev_character_damage 10", "/dev_character_defeat"} {
		for _, ordinary := range []bool{false, true} {
			h.submit(t, 7, -100, "healing_owner", command, false, ordinary)
			assertSilent()
			assertUnchanged(before)
		}
	}

	// Ignored probes still advance durable intake, so silence cannot hide a
	// stuck cursor or a command merely waiting to execute after this assertion.
	var cursor int64

	if err := db.QueryRowContext(ctx, `SELECT last_completed_id FROM runtime_cursors WHERE source='telegram'`).Scan(&cursor); err != nil || cursor != h.id {
		t.Fatalf("disabled commands did not complete intake: cursor=%d update=%d err=%v", cursor, h.id, err)
	}

	assertText(call("/character", false), "35/100")
	view := call("/inventory", false)
	assertText(view, "Healing potion (2)")
	stick := call(view.button(t, "4"), true)
	equipped := call(stick.button(t, "Equip"), true)
	assertText(equipped, "Equipped")
	view = call("/inventory", false)
	assertText(view, "Weapon: Stick")
	assertText(view, "Bonuses: +1 attack, +0 defense")
	assertHealthQuantity(7, 35, 2)
	assertHealthQuantity(8, 100, 3)
	h.stop(t)
}
