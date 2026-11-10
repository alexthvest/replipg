package app

import (
	"context"
	"errors"
	"log/slog"
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

type testCraftGenerator struct {
	calls     atomic.Int32
	started   chan struct{}
	exited    chan struct{}
	block     bool
	rejection bool
	fail      bool
}

func (g *testCraftGenerator) Generate(ctx context.Context, ingredients []domain.Ingredient) (domain.Outcome, error) {
	g.calls.Add(1)
	if g.started != nil {
		close(g.started)
	}
	if g.exited != nil {
		defer close(g.exited)
	}
	if len(ingredients) != 1 {
		return domain.Outcome{}, errors.New("unexpected ingredients")
	}
	if g.block {
		<-ctx.Done()
		return domain.Outcome{}, ctx.Err()
	}
	if g.fail {
		return domain.Outcome{}, errors.New("service unavailable")
	}
	if g.rejection {
		return domain.Outcome{Kind: domain.Uncraftable}, nil
	}
	return domain.Outcome{
		Kind: domain.Success,
		Item: &items.Snapshot{
			Version:   1,
			CatalogID: "placeholder",
			Mode:      items.Stackable,
			Rarity:    "common",
			English: items.Text{
				Name:        "Crafted <material>",
				Description: "A crafted material.",
			},
			Russian: items.Text{
				Name:        "Созданный материал",
				Description: "Материал для крафта.",
			},
			Components: items.Components{Material: true},
		},
	}, nil
}

func openCraftReview(t *testing.T, fake *fakeBot, router *telegram.Router, ctx context.Context, base int64) (int, string) {
	t.Helper()
	router.Handle(ctx, message(base, -1, 2, "/craft", 0))
	messageID := len(fake.views)
	menu := waitMenu(t, fake, -1, messageID, nil)
	router.Handle(ctx, callback(base+1, -1, 2, messageID, buttonData(t, menu, "Add")))
	materials := changedMenu(t, fake, -1, messageID, menu)
	router.Handle(ctx, callback(base+2, -1, 2, messageID, buttonData(t, materials, "1")))
	editor := changedMenu(t, fake, -1, messageID, materials)
	if len(editor.Rows[0]) != 6 {
		t.Fatalf("quantity row: %+v", editor.Rows)
	}
	router.Handle(ctx, callback(base+3, -1, 2, messageID, buttonData(t, editor, "Add")))
	composition := changedMenu(t, fake, -1, messageID, editor)
	router.Handle(ctx, callback(base+4, -1, 2, messageID, buttonData(t, composition, "Confirm")))
	review := changedMenu(t, fake, -1, messageID, composition)
	if !strings.Contains(textOf(review), "consum") {
		t.Fatalf("missing loss warning: %s", textOf(review))
	}
	return messageID, buttonData(t, review, "Confirm crafting")
}

func TestCraftingSuccessReplaysAndUpdatesInventory(t *testing.T) {
	cfg := testConfig(t)
	cfg.Crafting.Enabled = true
	generator := &testCraftGenerator{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 3", 2))
		router.Handle(ctx, message(2, -1, 2, "/inventory", 0))
		inventoryID := len(fake.views)
		waitMenu(t, fake, -1, inventoryID, nil)

		messageID, confirm := openCraftReview(t, fake, router, ctx, 10)
		fake.setEditError(errors.New("delivery unavailable"))
		router.Handle(ctx, callback(20, -1, 2, messageID, confirm))
		waitFailedCraftDelivery(t, fake)
		fake.setEditError(nil)
		result := waitMenu(t, fake, -1, messageID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Crafted <material>")
		})
		waitMenu(t, fake, -1, inventoryID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Crafted <material>")
		})
		router.Handle(ctx, callback(21, -1, 2, messageID, confirm))
		router.Handle(ctx, message(22, -1, 2, "/inventory", 0))
		inventory := waitMenu(t, fake, -1, len(fake.views), nil)
		if !strings.Contains(textOf(inventory), "Crafted <material>") {
			t.Fatalf("output not visible: %s", textOf(inventory))
		}
		if !strings.Contains(textOf(result), "crafted") {
			t.Fatal("missing success")
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
		t.Fatalf("generator calls %d", generator.calls.Load())
	}
	db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
	}()
	entries, err := db.LoadInventory(context.Background())
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries %+v: %v", entries, err)
	}
	for _, entry := range entries {
		if entry.Snapshot.CatalogID == "coin" && entry.Quantity != 2 {
			t.Fatalf("replay consumed twice: %+v", entry)
		}
		if strings.HasPrefix(entry.Snapshot.CatalogID, "generated:") && (entry.Quantity != 1 || !entry.Snapshot.Components.Material) {
			t.Fatalf("output %+v", entry)
		}
	}
}

func waitFailedCraftDelivery(t *testing.T, fake *fakeBot) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()

	for {
		attempted := false
		fake.mu.Lock()
		for _, view := range fake.edits {
			if strings.Contains(textOf(view), "Crafted <material>") {
				attempted = true
			}
		}
		fake.mu.Unlock()
		if attempted {
			return
		}

		select {
		case <-deadline.C:
			t.Fatal("committed output delivery was not attempted")
		case <-tick.C:
		}
	}
}

func TestCraftingShutdownCancelsAndJoinsSlowGeneration(t *testing.T) {
	cfg := testConfig(t)
	cfg.Crafting.Enabled = true
	generator := &testCraftGenerator{
		block:   true,
		started: make(chan struct{}),
		exited:  make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give coin 3", 2))
		messageID, confirm := openCraftReview(t, fake, router, ctx, 10)
		router.Handle(ctx, callback(20, -1, 2, messageID, confirm))
		select {
		case <-generator.started:
		case <-time.After(5 * time.Second):
			t.Fatal("generation did not start")
		}
		router.Handle(ctx, message(21, -1, 2, "/ping", 0))
		if textOf(fake.views[len(fake.views)-1]) != "Pong!" {
			t.Fatal("slow generation blocked gameplay")
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
	select {
	case <-generator.exited:
	default:
		t.Fatal("generation was not joined")
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
		t.Fatalf("interrupted discovery lost materials: %+v %v", entries, err)
	}
}

func TestCraftingTechnicalFailureConsumesNothing(t *testing.T) {
	for _, rejected := range []bool{
		false,
		true,
	} {
		name := "technical-failure"
		if rejected {
			name = "invalid-rejection-response"
		}
		t.Run(name, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.Crafting.Enabled = true
			generator := &testCraftGenerator{
				fail:      !rejected,
				rejection: rejected,
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fake := &fakeBot{}
			fake.script = func(ctx context.Context, router *telegram.Router) {
				defer cancel()
				router.Handle(ctx, message(1, -1, 1, "/give coin 3", 2))
				messageID, confirm := openCraftReview(t, fake, router, ctx, 10)
				router.Handle(ctx, callback(20, -1, 2, messageID, confirm))
				expected := "local model could not"
				waitMenu(t, fake, -1, messageID, func(view core.View) bool {
					return strings.Contains(textOf(view), expected)
				})
				router.Handle(ctx, callback(21, -1, 2, messageID, confirm))
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
				t.Fatalf("unexpected AI retries %d", generator.calls.Load())
			}
			db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = db.Close()
			}()
			entries, err := db.LoadInventory(context.Background())
			quantity := int64(3)
			if err != nil || len(entries) != 1 || entries[0].Quantity != quantity {
				t.Fatalf("incorrect ingredient effects %+v %v", entries, err)
			}
		})
	}
}
