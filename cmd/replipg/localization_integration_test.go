package main

import (
	"context"
	"encoding/json"
	"github.com/alexthvest/replipg/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Runs the production executable through a local TLS fake. All authorization,
// response rendering, SQLite persistence, and menu work uses production wiring.
func TestLocalizationProductionBinarySwitchRestartAndIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	binary := buildInventoryBinary(t, ctx, false)
	dir := t.TempDir()
	h := startInventoryBinary(t, ctx, binary, dir, 4, true)
	call := func(user, chat int64, data string, callback bool) inventoryBinaryReply {
		t.Helper()
		h.submit(t, user, chat, "Player_Name", data, callback)
		return h.reply(t, user, chat)
	}
	member := call(8, -100, "/settings", false)
	if len(member.buttons) != 0 || !strings.Contains(member.text, "Only group administrators") {
		t.Fatalf("member settings: %q", member.text)
	}
	db := inventoryBinaryDB(t, dir)
	var count int
	for _, table := range []string{"characters", "inventories"} {
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("settings changed %s: %d %v", table, count, err)
		}
	}
	_ = db.Close()
	admin := call(7, -100, "/settings", false)
	ru := admin.button(t, "Русский")
	h.denyAdmin.Store(true)
	denied := call(7, -100, ru, true)
	if !strings.Contains(denied.text, "Only group administrators") {
		t.Fatalf("lost administrator status: %q", denied.text)
	}
	h.denyAdmin.Store(false)
	h.rejectMenus.Store(true)
	h.rejectEdit.Store(true)
	selected := call(7, -100, ru, true)
	h.rejectEdit.Store(false)
	if !strings.Contains(selected.text, "Русский") || !strings.Contains(selected.text, "сохран") {
		t.Fatalf("Russian confirmation: %q", selected.text)
	}
	select {
	case <-h.menuFailures:
	case <-ctx.Done():
		t.Fatal("menu rejection not observed")
	}
	pending := call(7, -100, "/settings", false)
	if !strings.Contains(pending.text, "ожидается") {
		t.Fatalf("pending menu feedback: %q", pending.text)
	}
	character := call(7, -100, "/character", false)
	if !strings.Contains(character.text, "Здоровье") || !strings.Contains(character.text, "100/100") {
		t.Fatalf("Russian character: %q", character.text)
	}
	page := call(7, -100, "/inventory", false)
	if !strings.Contains(page.text, "Монета") || !strings.Contains(page.text, "Кожаная броня") || !strings.Contains(page.text, "Player_Name") {
		t.Fatalf("Russian inventory: %q", page.text)
	}
	oldItem := page.button(t, "4")
	second := call(7, -200, "/character", false)
	if !strings.Contains(second.text, "Health: 100/100") {
		t.Fatalf("group isolation: %q", second.text)
	}
	lastID := h.id
	h.stop(t)
	h = startInventoryBinary(t, ctx, binary, dir, 4, false, map[string]string{envDevCharacter: "true"})
	// Each fixture starts its own update counter; real durable intake resumes it.
	h.id = lastID
	waitLocalizedMenu(t, ctx, h, "Посмотреть персонажа", "character", "dev_character_damage", "dev_character_defeat", "inventory", "settings")
	restored := call(7, -100, "/settings", false)
	if !strings.Contains(restored.text, "Русский") {
		t.Fatalf("restart lost language: %q", restored.text)
	}
	call(7, -100, restored.button(t, "English"), true)
	waitLocalizedMenu(t, ctx, h, "Inspect your character", "character", "dev_character_damage", "dev_character_defeat", "inventory", "settings")
	detail := call(7, -100, oldItem, true)
	if !strings.Contains(detail.text, "Stick") || !strings.Contains(detail.text, "Equip") && len(detail.buttons) == 0 {
		t.Fatalf("old inventory control lost current language: %q", detail.text)
	}
	// Reusing a consumed Russian control must not undo the later English choice.
	stale := call(7, -100, ru, true)
	if !strings.Contains(stale.text, "Open /settings again") {
		t.Fatalf("reused control: %q", stale.text)
	}
	db = inventoryBinaryDB(t, dir)
	var locale string
	if err := db.QueryRowContext(ctx, `SELECT locale FROM group_settings WHERE chat_id=-100`).Scan(&locale); err != nil || locale != "en" {
		t.Fatalf("replay changed language: %q %v", locale, err)
	}
	_ = db.Close()
	h.stop(t)
}

func TestLocalizationInvalidCatalogPreventsAdmission(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	source, err := filepath.Abs("../../internal/localization/locales/en.json")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	replacement := filepath.Join(dir, "en.json")
	if err := os.WriteFile(replacement, []byte(`{"settings.title":"incomplete"}`), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, err := json.Marshal(map[string]any{"Replace": map[string]string{source: replacement}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "overlay.json")
	if err := os.WriteFile(path, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "replipg")
	if out, err := exec.CommandContext(ctx, "go", "build", "-overlay", path, "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build invalid catalog fixture: %s %v", out, err)
	}
	storage := t.TempDir()
	env := commandEnvironment()
	env[config.EnvStorageDir] = storage
	cmd := exec.CommandContext(ctx, binary)
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	out, err := cmd.CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "localization: en missing category.armor") {
		t.Fatalf("invalid catalog must fail construction: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(storage, "replipg.db")); !os.IsNotExist(err) {
		t.Fatal("invalid catalog reached database startup")
	}
}

func waitLocalizedMenu(t *testing.T, ctx context.Context, h *inventoryBinaryFixture, description string, names ...string) {
	t.Helper()
	for {
		select {
		case menu := <-h.menus:
			if len(menu) > 0 && menu[0].Description == description {
				if len(menu) != len(names) {
					t.Fatal("localized menu incomplete")
				}
				for index, command := range menu {
					if command.Command != names[index] {
						t.Fatal("localized menu lost enabled command")
					}
					if !command.Ephemeral {
						t.Fatal("localized menu changed ephemeral behavior")
					}
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("localized menu did not converge")
		}
	}
}
