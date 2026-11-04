package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
)

func TestInventoryPostCommitDeliveryAndPersonalPrivacy(t *testing.T) {
	dir := t.TempDir()
	options, err := productionOptions(config.Config{}, commandLookup(map[string]string{envDevInventory: "true", envDevCharacter: "true"}))
	if err != nil {
		t.Fatal(err)
	}

	h := startCharacterRuntime(t, dir, options, true)
	const label = "SensitiveInventoryActor_042"
	const privateArgument = "SensitiveInventoryCommand_042"

	update := map[string]any{"update_id": 1, "message": map[string]any{"ephemeral_message_id": 1, "date": time.Now().Unix(), "from": map[string]any{"id": 42, "username": label}, "chat": map[string]any{"id": -100, "type": "supergroup"}, "text": "/inventory", "entities": []map[string]any{{"type": "bot_command", "offset": 0, "length": 10}}}}
	data, err := json.Marshal(update)
	if err != nil {
		t.Fatal(err)
	}

	h.submit(t, string(data))
	var personal characterReply

	select {
	case personal = <-h.replies:
	case <-time.After(5 * time.Second):
		t.Fatal("inventory reply timeout")
	}

	if !strings.Contains(personal.text, "Inventory • "+label) {
		t.Fatal("transient owner header missing")
	}

	usage := h.command(t, 2, 42, -100, "/inventory "+privateArgument)

	if strings.Contains(usage.text, privateArgument) {
		t.Fatal("private argument echoed")
	}

	h.command(t, 3, 42, -100, "/dev_inventory_grant coin 2")
	h.command(t, 4, 42, -100, "/dev_character_damage 20")
	h.command(t, 5, 42, -100, "/dev_inventory_heal healing_potion")
	h.waitCursor(t, 5)
	// Ordinary messages do not create another character, even with sensitive metadata.
	h.submit(t, fmt.Sprintf(`{"update_id":6,"message":{"message_id":6,"date":%d,"from":{"id":43,"username":%q},"chat":{"id":-100,"type":"supergroup"},"text":"/inventory","entities":[{"type":"bot_command","offset":0,"length":10}]}}`, time.Now().Unix(), label))
	h.waitCursor(t, 6)
	select {
	case <-h.replies:
		t.Fatal("ordinary command or rejected delivery produced fallback")
	default:
	}

	var characters int

	if err := h.db.QueryRowContext(context.Background(), `SELECT count(*) FROM characters`).Scan(&characters); err != nil || characters != 1 {
		t.Fatal("ordinary command initialized character")
	}

	before := inventoryPrivateDurableState(t, h.db)
	for _, content := range before {
		if strings.Contains(content, label) || strings.Contains(content, privateArgument) || strings.Contains(content, "Inventory •") {
			t.Fatal("personal content persisted")
		}
	}

	rows, err := h.db.QueryContext(context.Background(), `SELECT token FROM inventory_controls`)
	if err != nil {
		t.Fatal(err)
	}

	var sensitive = []string{label, privateArgument, personal.text, "inventory:dev:", "healing_potion"}

	for rows.Next() {
		var token string

		if err := rows.Scan(&token); err != nil {
			t.Fatal(err)
		}

		sensitive = append(sensitive, token)
	}

	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}

	h.captureProbes(t)
	h.stop(t)
	h.assertNoSensitiveDiagnostics(t, sensitive...)
	h = startCharacterRuntime(t, dir, productionCharacterOptions(t), false)
	reopened := h.command(t, 7, 42, -200, "/inventory")

	if !strings.Contains(reopened.text, "Coin (12)") || !strings.Contains(reopened.text, "Healing potion (2)") || !strings.Contains(reopened.text, "Inventory • you") {
		t.Fatalf("committed effects lost on rejected delivery: %q", reopened.text)
	}

	character := h.command(t, 8, 42, -200, "/character")

	if !strings.Contains(character.text, "90/100") {
		t.Fatal("committed healing lost")
	}

	after := inventoryPrivateDurableState(t, h.db)
	// Controls are presentation state and may grow; immutable receipts and domain
	// state must retain exactly the committed result without duplicated effects.
	for table, contents := range before {
		if table != "inventory_controls" && !reflect.DeepEqual(after[table], contents) {
			t.Fatalf("reopening changed committed %s", table)
		}
	}

	h.captureProbes(t)
	h.stop(t)
	h.assertNoSensitiveDiagnostics(t, sensitive...)
}

type inventoryPrivacyDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func inventoryPrivateDurableState(t *testing.T, db inventoryPrivacyDB) map[string]string {
	t.Helper()
	state := map[string]string{}
	for _, table := range []string{"characters", "character_operations", "inventories", "inventory_catalog", "inventory_items", "inventory_equipment", "inventory_operations", "inventory_controls"} {
		rows, err := db.QueryContext(context.Background(), `SELECT * FROM `+table+` ORDER BY 1`)
		if err != nil {
			t.Fatal(err)
		}

		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}

		var values [][]any

		for rows.Next() {
			row := make([]any, len(columns))
			dest := make([]any, len(columns))
			for i := range row {
				dest[i] = &row[i]
			}

			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}

			values = append(values, row)
		}

		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}

		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}

		encoded, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}

		state[table] = string(encoded)
	}
	return state
}
