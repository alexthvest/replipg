package app

import (
	"context"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/ui/menus"
)

type craftMenuDriver struct {
	t      *testing.T
	fake   *fakeBot
	router *telegram.Router
	ctx    context.Context
	id     int
	update int64
	view   core.View
}

func (d *craftMenuDriver) click(caption string) core.View {
	d.t.Helper()
	previous := d.view
	d.router.Handle(d.ctx, callback(d.update, -1, 2, d.id, buttonData(d.t, d.view, caption)))
	d.update++
	d.view = changedMenu(d.t, d.fake, -1, d.id, previous)
	return d.view
}

func assertCraftRows(t *testing.T, view core.View, want ...[]string) {
	t.Helper()
	var got [][]string
	for _, row := range view.Rows {
		var captions []string
		for _, button := range row {
			captions = append(captions, button.Caption)
		}
		got = append(got, captions)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("keyboard %+v, want %+v", got, want)
	}
}

func TestCraftCompositionDraftAndLayout(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 30", 2))
		router.Handle(ctx, message(2, -1, 2, "/craft", 0))
		d := craftMenuDriver{
			t:      t,
			fake:   fake,
			router: router,
			ctx:    ctx,
			id:     len(fake.views),
			update: 30,
		}
		d.view = waitMenu(t, fake, -1, d.id, nil)
		assertCraftRows(t, d.view, []string{"Add", "Clear"}, []string{"Confirm", "Close"})
		router.Handle(ctx, callback(10, -1, 2, d.id, buttonData(t, d.view, "Confirm")))
		fake.mu.Lock()
		ack := fake.acks[len(fake.acks)-1]
		fake.mu.Unlock()
		if !strings.Contains(ack, "No ingredients") {
			t.Fatal("empty confirm did not ask for materials")
		}
		d.click("Add")
		stale := buttonData(t, d.view, "1")
		d.click("1")
		d.click("+10")
		d.click("Back")
		d.click("Back")
		assertCraftRows(t, d.view, []string{"Add", "Clear"}, []string{"Confirm", "Close"})
		router.Handle(ctx, callback(11, -1, 2, d.id, stale))
		if !reflect.DeepEqual(d.view, waitMenu(t, fake, -1, d.id, nil)) {
			t.Fatal("superseded material callback changed composition")
		}
		d.click("Add")
		d.click("1")
		d.click("+10")
		d.click("Add")
		assertCraftRows(t, d.view, []string{"1"}, []string{"Add", "Clear"}, []string{"Confirm", "Close"})
		d.click("1")
		d.click("+1")
		d.click("Back")
		if !strings.Contains(textOf(d.view), "× 11") || strings.Contains(textOf(d.view), "× 12") {
			t.Fatal("Back committed draft edit")
		}
		d.click("Add")
		d.click("1")
		if !hasShopButton(d.view, "Save") || !strings.Contains(textOf(d.view), "Quantity: 11") {
			t.Fatal("existing material was duplicated instead of edited")
		}
		d.click("+1")
		d.click("Save")
		if !strings.Contains(textOf(d.view), "× 12") {
			t.Fatal("Save did not commit draft")
		}
		d.click("Clear")
		assertCraftRows(t, d.view, []string{"Add", "Clear"}, []string{"Confirm", "Close"})
		d.click("Close")
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
}

func TestCraftResultActions(t *testing.T) {
	for _, action := range []string{"Repeat", "Repeat available", "New craft", "Use in crafting", "Inventory"} {
		t.Run(action, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.Crafting.Enabled = true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := &fakeBot{}
			generator := &testCraftGenerator{}
			fake.script = func(ctx context.Context, router *telegram.Router) {
				defer cancel()
				quantity := "1"
				if action == "Repeat available" {
					quantity = "2"
				}
				router.Handle(ctx, message(1, -1, 1, "/give coin "+quantity, 2))
				id, confirm := openCraftReview(t, fake, router, ctx, 10)
				router.Handle(ctx, callback(20, -1, 2, id, confirm))
				result := waitMenu(t, fake, -1, id, func(view core.View) bool {
					return strings.Contains(textOf(view), "Crafted <material>")
				})
				assertCraftRows(t, result, []string{"Use in crafting"}, []string{"Repeat", "New craft"}, []string{"Inventory", "Close"})
				if !strings.Contains(textOf(result), "Consumed:") || !strings.Contains(textOf(result), "Coin × 1") {
					t.Fatal("success lost consumed snapshots")
				}
				d := craftMenuDriver{
					t:      t,
					fake:   fake,
					router: router,
					ctx:    ctx,
					id:     id,
					update: 30,
					view:   result,
				}
				caption := action
				if action == "Repeat available" {
					caption = "Repeat"
				}
				d.click(caption)
				switch action {
				case "Repeat":
					if !strings.Contains(textOf(d.view), "Coin × 1") || !strings.Contains(textOf(d.view), "Available: 0") {
						t.Fatal("Repeat lost the exhausted ingredient")
					}
					d.click("Confirm")
					assertCraftRows(t, d.view, []string{"Change composition", "Clear"}, []string{"Close"})
					d.click("Change composition")
					d.click("1")
					d.click("Remove")
				case "Repeat available":
					if generator.calls.Load() != 1 || !strings.Contains(textOf(d.view), "Coin × 1") {
						t.Fatal("Repeat submitted immediately or changed ingredients")
					}
					d.click("Confirm")
					d.click("Confirm crafting")
					d.view = waitMenu(t, fake, -1, id, func(view core.View) bool {
						return strings.Contains(textOf(view), "Item created.")
					})
					router.Handle(ctx, message(50, -1, 2, "/inventory", 0))
					inventoryView := waitMenu(t, fake, -1, len(fake.views), nil)
					if !strings.Contains(textOf(inventoryView), "(2)") || strings.Contains(textOf(inventoryView), "Coin") {
						t.Fatal("intentional repeat did not consume once and reuse the saved output")
					}
				case "New craft":
					if !strings.Contains(textOf(d.view), "No ingredients") {
						t.Fatal("New craft retained consumed composition")
					}
				case "Use in crafting":
					if !strings.Contains(textOf(d.view), "1. Crafted <material> × 1") {
						t.Fatal("output was not selected")
					}
				case "Inventory":
					if !strings.Contains(textOf(d.view), "Inventory") || hasShopButton(d.view, "Confirm") {
						t.Fatal("Inventory retained crafting controls")
					}
					d.click("1")
					if !strings.Contains(textOf(d.view), "A crafted material.") {
						t.Fatal("inventory inspection unavailable")
					}
				}
				// An old accepted confirmation must neither mutate nor navigate a new draft.
				router.Handle(ctx, callback(60, -1, 2, id, confirm))
				if !reflect.DeepEqual(d.view, waitMenu(t, fake, -1, id, nil)) {
					t.Fatal("accepted old confirmation replaced current navigation")
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
				t.Fatal("result action started another generation")
			}
		})
	}
}

func TestCraftRetryRequiresFreshConfirmation(t *testing.T) {
	cfg := testConfig(t)
	cfg.Crafting.Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	generator := &testCraftGenerator{fail: true}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 2", 2))
		id, confirm := openCraftReview(t, fake, router, ctx, 10)
		router.Handle(ctx, callback(20, -1, 2, id, confirm))
		failed := waitMenu(t, fake, -1, id, func(view core.View) bool {
			return strings.Contains(textOf(view), "local model could not")
		})
		assertCraftRows(t, failed, []string{"Retry"}, []string{"Change composition", "Close"})
		d := craftMenuDriver{
			t:      t,
			fake:   fake,
			router: router,
			ctx:    ctx,
			id:     id,
			update: 30,
			view:   failed,
		}
		d.click("Retry")
		fresh := buttonData(t, d.view, "Confirm crafting")
		if fresh == confirm || generator.calls.Load() != 1 {
			t.Fatal("Retry submitted before confirmation or reused old token")
		}
		router.Handle(ctx, callback(40, -1, 2, id, confirm))
		if !reflect.DeepEqual(d.view, waitMenu(t, fake, -1, id, nil)) {
			t.Fatal("old confirmation replaced fresh review")
		}
		router.Handle(ctx, callback(41, -1, 2, id, fresh))
		waitMenu(t, fake, -1, id, func(view core.View) bool {
			return strings.Contains(textOf(view), "local model could not")
		})
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
	if generator.calls.Load() != 2 {
		t.Fatalf("explicit retry calls: %d", generator.calls.Load())
	}
}

func TestCraftClosePendingPreservesFulfillment(t *testing.T) {
	cfg := testConfig(t)
	cfg.Crafting.Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	generator := &languageCraftGenerator{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 2", 2))
		router.Handle(ctx, message(2, -1, 2, "/inventory", 0))
		inventoryID := len(fake.views)
		waitMenu(t, fake, -1, inventoryID, nil)
		id, confirm := openCraftReview(t, fake, router, ctx, 10)
		router.Handle(ctx, callback(20, -1, 2, id, confirm))
		select {
		case <-generator.started:
		case <-time.After(5 * time.Second):
			t.Fatal("generation did not start")
		}
		pending := waitMenu(t, fake, -1, id, func(view core.View) bool {
			return strings.Contains(textOf(view), "Crafting is pending")
		})
		assertCraftRows(t, pending, []string{"Refresh", "Close"})
		router.Handle(ctx, callback(21, -1, 2, id, buttonData(t, pending, "Close")))
		closed := changedMenu(t, fake, -1, id, pending)
		if len(closed.Rows) != 0 {
			t.Fatal("closed menu kept controls")
		}
		close(generator.release)
		waitMenu(t, fake, -1, inventoryID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Saved English result")
		})
		router.Handle(ctx, callback(22, -1, 2, id, confirm))
		if !reflect.DeepEqual(closed, waitMenu(t, fake, -1, id, nil)) {
			t.Fatal("completed attempt reopened closed menu")
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
}

func TestCraftResultControlsOmitNonmaterialAction(t *testing.T) {
	var keyboard craftKeyboard
	m := &modules{}
	m.craftResultControls(&keyboard, domain.Attempt{State: domain.Success}, func(key, kind string) craftControl {
		return craftControl{
			caption: key,
			action:  menus.Action{Kind: kind},
		}
	})
	if len(keyboard.rows) != 2 || keyboard.rows[0][0].caption != "repeat" {
		t.Fatal("nonmaterial result offers reuse")
	}
}

func TestCraftCompositionPagesAndIndividualEditor(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		for i, id := range []string{"stick", "coin", "fumo", "wood_plank", "stone", "clay", "sand", "iron_ingot"} {
			router.Handle(ctx, message(int64(i+1), -1, 1, "/give "+id+" 1", 2))
		}
		router.Handle(ctx, message(10, -1, 2, "/craft", 0))
		d := craftMenuDriver{
			t:      t,
			fake:   fake,
			router: router,
			ctx:    ctx,
			id:     len(fake.views),
			update: 30,
		}
		d.view = waitMenu(t, fake, -1, d.id, nil)
		for i := range 8 {
			d.click("Add")
			if i == 6 {
				d.click("Next")
			}
			d.click(strconv.Itoa(i%6 + 1))
			if i == 0 && (hasShopButton(d.view, "+1") || hasShopButton(d.view, "Max")) {
				t.Fatal("individual material offered quantity controls")
			}
			d.click("Add")
		}
		assertCraftRows(t, d.view,
			[]string{"1", "2", "3", "4", "5", "6"},
			[]string{"Next"},
			[]string{"Add", "Clear"},
			[]string{"Confirm", "Close"},
		)
		d.click("Next")
		assertCraftRows(t, d.view,
			[]string{"1", "2"},
			[]string{"Previous"},
			[]string{"Add", "Clear"},
			[]string{"Confirm", "Close"},
		)
		d.click("1")
		d.click("Back")
		if !strings.Contains(textOf(d.view), "Page 2/2") {
			t.Fatal("composition editor lost its page")
		}
		d.click("Add")
		if !strings.Contains(textOf(d.view), "Page 2/2") {
			t.Fatal("browser page lost")
		}
		d.click("2")
		d.click("Back")
		if !strings.Contains(textOf(d.view), "Page 2/2") {
			t.Fatal("browser editor lost its origin page")
		}
		d.click("Back")
		d.click("Confirm")
		if strings.Count(textOf(d.view), " × ") != 6 {
			t.Fatal("review did not page the complete composition")
		}
		d.click("Next")
		if strings.Count(textOf(d.view), " × ") != 2 {
			t.Fatal("review lost seventh and eighth ingredients")
		}
		d.click("Close")
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
}

func TestCraftUnavailableOutputCannotSeedComposition(t *testing.T) {
	cfg := testConfig(t)
	cfg.Crafting.Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	generator := &testCraftGenerator{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 1", 2))
		id, confirm := openCraftReview(t, fake, router, ctx, 10)
		router.Handle(ctx, callback(20, -1, 2, id, confirm))
		result := waitMenu(t, fake, -1, id, func(view core.View) bool {
			return strings.Contains(textOf(view), "Item created.")
		})
		// The original output is now the only available material in the inventory.
		secondID, secondConfirm := openCraftReview(t, fake, router, ctx, 30)
		router.Handle(ctx, callback(40, -1, 2, secondID, secondConfirm))
		waitMenu(t, fake, -1, secondID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Item created.")
		})
		router.Handle(ctx, callback(41, -1, 2, id, buttonData(t, result, "Use in crafting")))
		fake.mu.Lock()
		ack := fake.acks[len(fake.acks)-1]
		fake.mu.Unlock()
		if !strings.Contains(ack, "no longer available") || !reflect.DeepEqual(result, waitMenu(t, fake, -1, id, nil)) {
			t.Fatal("consumed output was selected for another recipe")
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
	if generator.calls.Load() != 2 {
		t.Fatal("reuse of unavailable output triggered another attempt")
	}
}

func TestCraftDraftSurvivesInventoryAndLanguageRefresh(t *testing.T) {
	cfg := testConfig(t)
	cfg.Crafting.Enabled = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 2", 2))
		router.Handle(ctx, message(2, -1, 2, "/craft", 0))
		d := craftMenuDriver{
			t:      t,
			fake:   fake,
			router: router,
			ctx:    ctx,
			id:     len(fake.views),
			update: 30,
		}
		d.view = waitMenu(t, fake, -1, d.id, nil)
		d.click("Add")
		d.click("1")
		d.click("Add")
		d.click("1")
		d.click("+1")
		// A separate accepted craft consumes one unit while the first editor is open.
		secondID, confirm := openCraftReview(t, fake, router, ctx, 10)
		router.Handle(ctx, callback(20, -1, 2, secondID, confirm))
		waitMenu(t, fake, -1, secondID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Item created.")
		})
		d.view = waitMenu(t, fake, -1, d.id, func(view core.View) bool {
			return strings.Contains(textOf(view), "Quantity: 2") && strings.Contains(textOf(view), "Available: 1")
		})
		if !hasShopButton(d.view, "Save") {
			t.Fatal("availability refresh lost editor")
		}
		router.Handle(ctx, message(21, -1, 1, "/settings", 0))
		settingsID := len(fake.views)
		settings := waitMenu(t, fake, -1, settingsID, nil)
		router.Handle(ctx, callback(22, -1, 1, settingsID, settings.Rows[1][0].Data))
		d.view = waitMenu(t, fake, -1, d.id, func(view core.View) bool {
			return strings.Contains(textOf(view), "Количество: 2") && strings.Contains(textOf(view), "Доступно: 1")
		})
		if hasShopButton(d.view, "Save") || !hasShopButton(d.view, "Сохранить") {
			t.Fatal("language refresh retained old labels")
		}
		d.click("Назад")
		if !strings.Contains(textOf(d.view), "Монета × 1") || strings.Contains(textOf(d.view), "Монета × 2") {
			t.Fatal("refresh or Back committed the draft")
		}
		assertCraftRows(t, d.view, []string{"1"}, []string{"Добавить", "Очистить"}, []string{"Подтвердить", "Закрыть"})
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
