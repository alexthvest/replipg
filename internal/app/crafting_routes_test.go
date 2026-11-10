package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/items"
)

func TestCraftRoutesDisabledAndCancelDoNotConsume(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 3", 2))
		messageID, confirm := openCraftReview(t, fake, router, ctx, 10)
		review := waitMenu(t, fake, -1, messageID, nil)
		for _, foreign := range []struct{ group, actor int64 }{{-2, 2}, {-1, 3}} {
			router.Handle(ctx, callback(30+foreign.actor, foreign.group, foreign.actor, messageID, confirm))
			if !reflect.DeepEqual(review, waitMenu(t, fake, -1, messageID, nil)) {
				t.Fatal("foreign callback changed crafting review")
			}
		}
		router.Handle(ctx, callback(40, -1, 2, messageID, confirm))
		fake.mu.Lock()
		acknowledgement := fake.acks[len(fake.acks)-1]
		fake.mu.Unlock()
		if !strings.Contains(acknowledgement, "disabled") {
			t.Fatalf("disabled discovery feedback: %q", acknowledgement)
		}
		disabled := changedMenu(t, fake, -1, messageID, review)
		router.Handle(ctx, callback(41, -1, 2, messageID, buttonData(t, disabled, "Close")))
		cancelled := changedMenu(t, fake, -1, messageID, disabled)
		if !strings.Contains(textOf(cancelled), "closed") || len(cancelled.Rows) != 0 {
			t.Fatalf("cancel view %+v", cancelled)
		}
		router.Handle(ctx, callback(42, -1, 2, messageID, confirm))
		if !reflect.DeepEqual(cancelled, waitMenu(t, fake, -1, messageID, nil)) {
			t.Fatal("stale confirmation reopened cancelled crafting")
		}
	}
	deps := defaults()
	deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
		return fake, nil
	}
	application, err := NewWith(cfg, logger(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.Run(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
	}()
	entries, err := db.LoadInventory(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Quantity != 3 {
		t.Fatalf("disabled/cancelled flow mutated inventory: %+v %v", entries, err)
	}
}

func TestCraftRoutesAccumulateManyTypesAndPreserveMaterialPage(t *testing.T) {
	cfg := testConfig(t)
	cfg.AssetsPath = appAssets(t)
	for i := range 8 {
		definition := fmt.Sprintf("package items\nmaterial%d: {name: \"item.coin.name\", description: \"item.coin.description\", rarity: \"common\", mode: \"stackable\", components: material: {}}\n", i)
		if err := os.WriteFile(filepath.Join(cfg.AssetsPath, "items", fmt.Sprintf("material%d.cue", i)), []byte(definition), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		for i := range 8 {
			router.Handle(ctx, message(int64(i+1), -1, 1, fmt.Sprintf("/give material%d 20", i), 2))
		}
		router.Handle(ctx, message(20, -1, 2, "/craft", 0))
		messageID := len(fake.views)
		menu := waitMenu(t, fake, -1, messageID, nil)
		update := int64(30)
		click := func(caption string) core.View {
			previous := menu
			router.Handle(ctx, callback(update, -1, 2, messageID, buttonData(t, menu, caption)))
			update++
			menu = changedMenu(t, fake, -1, messageID, previous)
			return menu
		}
		click("Add")
		click("1")
		if len(menu.Rows[0]) != 6 {
			t.Fatalf("quantity row: %+v", menu.Rows)
		}
		for i, caption := range []string{"1", "-10", "-1", "+1", "+10", "Max"} {
			if menu.Rows[0][i].Caption != caption {
				t.Fatalf("quantity control order: %+v", menu.Rows[0])
			}
		}
		click("+10")
		click("Add")
		click("Add")
		click("2")
		click("Add")
		click("Add")
		click("Next")
		if !strings.Contains(textOf(menu), "Page 2/2") {
			t.Fatal("material page missing")
		}
		click("1")
		click("Add")
		click("Confirm")
		if strings.Count(textOf(menu), " × ") != 3 || !strings.Contains(textOf(menu), "× 11") {
			t.Fatalf("selection not accumulated: %s", textOf(menu))
		}
		oldConfirm := buttonData(t, menu, "Confirm crafting")
		click("Back")
		click("Add")
		if !strings.Contains(textOf(menu), "Page 2/2") {
			t.Fatal("material browser lost its page")
		}
		click("Back")
		click("3")
		click("Remove")
		click("Confirm")
		if strings.Count(textOf(menu), " × ") != 2 || !strings.Contains(textOf(menu), "× 11") {
			t.Fatalf("remove affected other materials: %s", textOf(menu))
		}
		current := menu
		router.Handle(ctx, callback(update, -1, 2, messageID, oldConfirm))
		if !reflect.DeepEqual(current, waitMenu(t, fake, -1, messageID, nil)) {
			t.Fatal("old selection confirmation remained usable")
		}
		update++
		click("Close")
	}
	deps := defaults()
	deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
		return fake, nil
	}
	application, err := NewWith(cfg, logger(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.Run(ctx); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
	}()
	entries, err := db.LoadInventory(context.Background())
	if err != nil || len(entries) != 8 {
		t.Fatalf("menu flow changed material entries: %+v %v", entries, err)
	}
	for _, entry := range entries {
		if entry.Quantity != 20 {
			t.Fatal("unconfirmed menu consumed materials")
		}
	}
}

func TestCraftRoutesCanRemoveDisappearedSelection(t *testing.T) {
	cfg := testConfig(t)
	cfg.Crafting.Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 1", 2))
		router.Handle(ctx, message(2, -1, 2, "/craft", 0))
		firstID := len(fake.views)
		first := waitMenu(t, fake, -1, firstID, nil)
		router.Handle(ctx, callback(3, -1, 2, firstID, buttonData(t, first, "Add")))
		materials := changedMenu(t, fake, -1, firstID, first)
		router.Handle(ctx, callback(4, -1, 2, firstID, buttonData(t, materials, "1")))
		editor := changedMenu(t, fake, -1, firstID, materials)
		router.Handle(ctx, callback(5, -1, 2, firstID, buttonData(t, editor, "Add")))
		changedMenu(t, fake, -1, firstID, editor)
		secondID, confirm := openCraftReview(t, fake, router, ctx, 10)
		router.Handle(ctx, callback(20, -1, 2, secondID, confirm))
		waitMenu(t, fake, -1, secondID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Crafted <material>")
		})
		revised := waitMenu(t, fake, -1, firstID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Available: 0")
		})
		router.Handle(ctx, callback(21, -1, 2, firstID, buttonData(t, revised, "1")))
		missing := changedMenu(t, fake, -1, firstID, revised)
		router.Handle(ctx, callback(22, -1, 2, firstID, buttonData(t, missing, "Remove")))
		cleared := changedMenu(t, fake, -1, firstID, missing)
		if hasShopButton(cleared, "1") || !strings.Contains(textOf(cleared), "No ingredients") {
			t.Fatalf("disappeared selection could not be revised: %+v", cleared)
		}

	}
	deps := defaults()
	deps.Generator = &testCraftGenerator{}
	deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
		return fake, nil
	}
	application, err := NewWith(cfg, logger(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.Run(ctx); err != nil {
		t.Fatal(err)
	}
}

// languageCraftGenerator lets the test change live locale while generation waits.
type languageCraftGenerator struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (g *languageCraftGenerator) Generate(ctx context.Context, _ []domain.Ingredient) (domain.Outcome, error) {
	g.calls.Add(1)
	close(g.started)
	select {
	case <-g.release:
	case <-ctx.Done():
		return domain.Outcome{}, ctx.Err()
	}
	return domain.Outcome{
		Kind: domain.Success,
		Item: &items.Snapshot{
			Version:   1,
			CatalogID: "placeholder",
			Mode:      items.Stackable,
			Rarity:    "common",
			English: items.Text{
				Name:        "Saved English result",
				Description: "Saved English description.",
			},
			Russian: items.Text{
				Name:        "Сохранённый результат",
				Description: "Сохранённое русское описание.",
			},
		},
	}, nil
}

func TestCraftRoutesPendingLanguageChange(t *testing.T) {
	cfg := testConfig(t)
	cfg.Crafting.Enabled = true
	generator := &languageCraftGenerator{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 2", 2))
		messageID, confirm := openCraftReview(t, fake, router, ctx, 10)
		router.Handle(ctx, callback(20, -1, 2, messageID, confirm))
		select {
		case <-generator.started:
		case <-time.After(5 * time.Second):
			t.Fatal("generation did not start")
		}
		waitMenu(t, fake, -1, messageID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Crafting is pending")
		})

		router.Handle(ctx, message(21, -1, 1, "/settings", 0))
		fake.mu.Lock()
		settingsID := len(fake.views)
		fake.mu.Unlock()
		settings := waitMenu(t, fake, -1, settingsID, nil)
		router.Handle(ctx, callback(22, -1, 1, settingsID, settings.Rows[1][0].Data))
		pending := waitMenu(t, fake, -1, messageID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Создание ожидается")
		})
		if hasShopButton(pending, "Refresh") || !hasShopButton(pending, "Обновить") {
			t.Fatalf("pending controls did not use current language: %+v", pending.Rows)
		}

		close(generator.release)
		result := waitMenu(t, fake, -1, messageID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Сохранённый результат")
		})
		text := textOf(result)
		if !strings.Contains(text, "Предмет создан") || !strings.Contains(text, "Сохранённое русское описание.") || strings.Contains(text, "Saved English") {
			t.Fatalf("result ignored saved bilingual text or current language: %s", text)
		}
	}
	deps := defaults()
	deps.Generator = generator
	deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
		return fake, nil
	}
	application, err := NewWith(cfg, logger(), deps)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if generator.calls.Load() != 1 {
		t.Fatalf("language change repeated generation: %d calls", generator.calls.Load())
	}
}
