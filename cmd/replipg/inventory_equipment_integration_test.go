package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Supply a second weapon only in this executable's embedded test catalog. No
// production flag, reward route or version-controlled tuning is changed.
func buildEquipmentBinary(t *testing.T, ctx context.Context) string {
	t.Helper()
	source, err := filepath.Abs("../../internal/item/items.json")
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}

	var catalog struct {
		Items   []map[string]any `json:"items"`
		Starter []map[string]any `json:"starter"`
	}

	if err = json.Unmarshal(raw, &catalog); err != nil {
		t.Fatal(err)
	}

	starterSource, err := filepath.Abs("../../internal/inventory/starter.json")
	if err != nil {
		t.Fatal(err)
	}

	starterRaw, err := os.ReadFile(starterSource)
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal(starterRaw, &catalog); err != nil {
		t.Fatal(err)
	}

	catalog.Items = append(catalog.Items, map[string]any{"id": "sword", "category": "weapon", "attack_bonus": 3})
	catalog.Starter = append(catalog.Starter, map[string]any{"item_id": "sword", "quantity": 2})
	raw, err = json.Marshal(struct {
		Items []map[string]any `json:"items"`
	}{catalog.Items})
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	replacement := filepath.Join(dir, "items.json")
	if err = os.WriteFile(replacement, raw, 0600); err != nil {
		t.Fatal(err)
	}

	replacements := localizedItemOverlay(t, source, replacement, map[string][2]string{"sword": {"Sword", "Equipment replacement fixture."}})
	starterRaw, err = json.Marshal(struct {
		Starter []map[string]any `json:"starter"`
	}{catalog.Starter})
	if err != nil {
		t.Fatal(err)
	}

	starterReplacement := filepath.Join(t.TempDir(), "starter.json")
	if err := os.WriteFile(starterReplacement, starterRaw, 0600); err != nil {
		t.Fatal(err)
	}

	replacements[starterSource] = starterReplacement

	overlay, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "overlay.json")
	if err = os.WriteFile(path, overlay, 0600); err != nil {
		t.Fatal(err)
	}

	binary := filepath.Join(dir, "replipg")
	if output, err := exec.CommandContext(ctx, "go", "build", "-overlay", path, "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build equipment executable: %v\n%s", err, output)
	}

	return binary
}

func TestInventoryProductionBinaryEquipmentMilestone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	binary := buildEquipmentBinary(t, ctx)
	dir := t.TempDir()
	h := startInventoryBinary(t, ctx, binary, dir, 5, true)
	call := func(chat int64, data string, callback bool) inventoryBinaryReply {
		t.Helper()
		h.submit(t, 7, chat, "equipment_owner", data, callback)
		return h.reply(t, 7, chat)
	}
	assertText := func(reply inventoryBinaryReply, texts ...string) {
		t.Helper()
		for _, text := range texts {
			if !strings.Contains(reply.text, text) {
				t.Fatalf("missing %q in %q", text, reply.text)
			}
		}
	}
	first := call(-100, "/inventory", false)
	base := call(-100, "/character", false)
	db := inventoryBinaryDB(t, dir)
	defer func() { _ = db.Close() }()

	// Capture every persisted character field: inventory must not revise health,
	// recovery or base combat attributes as a side effect of equipment changes.
	characterState := func() string {
		t.Helper()
		var state string

		if err := db.QueryRowContext(ctx, `SELECT json_array(id,user_id,max_health,health,attack,defense,recover_at_ms,recovery_health,revision) FROM characters WHERE user_id=7`).Scan(&state); err != nil {
			t.Fatal(err)
		}

		return state
	}
	before := characterState()
	stick := call(-100, first.button(t, "4"), true)
	equipStick := stick.button(t, "Equip")
	other := call(-200, "/inventory", false)
	otherStick := call(-200, other.button(t, "4"), true)
	stale := otherStick.button(t, "Equip")
	// A mutation control remains bound to its original actor and group.
	h.submit(t, 8, -100, "foreign", equipStick, true)
	h.submit(t, 7, -200, "equipment_owner", equipStick, true)
	select {
	case <-h.replies:
		t.Fatal("foreign equipment control disclosed inventory")
	default:
	}

	assertText(call(-100, equipStick, true), "Owned: (1)", "\nEquipped")
	assertText(call(-200, stale, true), "Inventory changed")
	page := call(-100, "/inventory", false)
	assertText(page, "Stick (1) • Equipped", "Weapon: Stick (+1 attack)", "Bonuses: +1 attack, +0 defense")
	sword := call(-100, page.button(t, "5"), true)
	replaced := call(-100, sword.button(t, "Equip"), true)
	assertText(replaced, "Sword • equipment_owner", "Owned: (2)", "\nEquipped")
	page = call(-100, "/inventory", false)
	assertText(page, "Weapon: Sword (+3 attack)", "Bonuses: +3 attack, +0 defense", "4. Stick (1)\n")
	armor := call(-100, page.button(t, "1"), true)
	assertText(call(-100, armor.button(t, "Equip"), true), "\nEquipped")
	page = call(-100, "/inventory", false)
	assertText(page, "Bonuses: +3 attack, +1 defense")
	// Read the Sword again after armor advances the aggregate revision.
	sword = call(-100, page.button(t, "5"), true)
	assertText(call(-100, sword.button(t, "Unequip"), true), "Owned: (2)", "Unequipped")
	assertText(call(-100, equipStick, true), "Stick • equipment_owner", "Unequipped")
	if got := call(-100, "/character", false); got.text != base.text {
		t.Fatal("equipment changed character UI")
	}

	h.stop(t)
	next := startInventoryBinary(t, ctx, binary, dir, 5, false)
	next.id = h.id
	h = next
	assertText(call(-100, equipStick, true), "Stick • equipment_owner", "Unequipped")
	assertText(call(-200, stale, true), "Inventory changed")
	page = call(-200, "/inventory", false)
	assertText(page, "Weapon: Empty", "Armor: Leather armor (+1 defense)", "Bonuses: +0 attack, +1 defense", "Stick (1)", "Sword (2)")
	h.stop(t)

	if characterState() != before {
		t.Fatal("equipment altered persisted character base state")
	}

	for query, want := range map[string]int{
		`SELECT count(*) FROM inventory_equipment WHERE slot='armor' AND item_id='cloth_armor'`: 1,
		`SELECT count(*) FROM inventory_equipment`:                                              1,
		`SELECT count(*) FROM inventory_operations WHERE kind='equipment'`:                      4,
		`SELECT count(*) FROM inventory_operations WHERE kind='starter'`:                        1,
		`SELECT revision FROM inventories`:                                                      6,
		`SELECT count(*) FROM inventory_items WHERE (item_id='coin' AND quantity=10) OR (item_id='stick' AND quantity=1) OR (item_id='cloth_armor' AND quantity=1) OR (item_id='healing_potion' AND quantity=3) OR (item_id='sword' AND quantity=2)`: 5,
	} {
		var got int

		if err := db.QueryRowContext(ctx, query).Scan(&got); err != nil || got != want {
			t.Fatalf("equipment persistence: got %d want %d err %v", got, want, err)
		}
	}
}
