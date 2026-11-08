package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/ui/views"
)

func TestItemsOwnerOnlyNavigationAndLanguage(t *testing.T) {
	cfg := testConfig(t)
	bundle, err := cueassets.Load(cfg.AssetsPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake := &fakeBot{}
	fake.script = func(ctx context.Context, r *telegram.Router) {
		defer cancel()

		r.Handle(ctx, message(1, -1, 1, "/admin add", 2))
		for _, actor := range []int64{2, 3} {
			r.Handle(ctx, message(2, -1, actor, "/items", 0))
			if !strings.Contains(textOf(fake.views[len(fake.views)-1]), "permission") {
				t.Error("non-owner accessed catalog")
			}
		}

		r.Handle(ctx, message(3, -1, 1, "/items unexpected", 0))
		if !strings.Contains(textOf(fake.views[len(fake.views)-1]), "Use /items") {
			t.Error("arguments did not produce usage")
		}

		r.Handle(ctx, message(4, -1, 1, "/items@replipg_bot", 0))
		menuID := len(fake.views)
		menu := waitMenu(t, fake, -1, menuID, nil)
		if menu.Text[0].Text != "Items" || len(menu.Rows) != 2 || len(menu.Rows[0]) != 6 {
			t.Error("catalog missing navigation")
			return
		}

		next := menu.Rows[1][0].Data
		before := fake.editCount()
		r.Handle(ctx, callback(5, -1, 2, menuID, next))
		r.Handle(ctx, callback(6, -1, 3, menuID, next))
		r.Handle(ctx, callback(7, -2, 1, menuID, next))
		r.Handle(ctx, callback(8, -1, 1, menuID+1, next))
		if fake.editCount() != before {
			t.Error("foreign callback changed catalog page")
		}

		listed := textOf(menu)
		r.Handle(ctx, callback(9, -1, 1, menuID, next))
		menu = changedMenu(t, fake, -1, menuID, menu)
		listed += textOf(menu)
		for _, id := range bundle.ItemIDs() {
			if !strings.Contains(listed, id) {
				t.Errorf("item %s unreachable", id)
			}
		}

		before = fake.editCount()
		r.Handle(ctx, callback(10, -1, 1, menuID, next))
		if fake.editCount() != before {
			t.Error("stale page action accepted")
		}

		previous := menu.Rows[1][0].Data
		r.Handle(ctx, message(11, -1, 1, "/settings", 0))
		settings := waitMenu(t, fake, -1, len(fake.views), nil)
		r.Handle(ctx, callback(12, -1, 1, len(fake.views), settings.Rows[1][0].Data))
		r.Handle(ctx, callback(13, -1, 1, menuID, previous))
		menu = waitMenu(t, fake, -1, menuID, func(v core.View) bool {
			return v.Text[0].Text == "Предметы"
		})
		if menu.Text[0].Text != "Предметы" || !strings.Contains(textOf(menu), "Монета") {
			t.Error("catalog did not use current Russian locale")
		}

		r.Handle(ctx, message(14, -2, 1, "/items", 0))
		if waitMenu(t, fake, -2, len(fake.views), nil).Text[0].Text != "Items" {
			t.Error("catalog language leaked across groups")
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

	characters, err := db.LoadCharacters(context.Background())
	if err != nil || len(characters) != 0 {
		t.Fatalf("catalog created characters: %v %v", characters, err)
	}

	entries, err := db.LoadInventory(context.Background())
	if err != nil || len(entries) != 0 {
		t.Fatalf("catalog changed ownership: %v %v", entries, err)
	}
}

func TestItemsButtonsGrantToOwnerAndProtectReplays(t *testing.T) {
	cfg := testConfig(t)
	cfg.AssetsPath = appAssets(t)
	bundle, err := cueassets.Load(cfg.AssetsPath)
	if err != nil {
		t.Fatal(err)
	}

	ids := bundle.ItemIDs()
	coinIndex := slices.Index(ids, "coin")
	stickIndex := slices.Index(ids, "stick")
	if coinIndex < 0 || coinIndex >= views.PageSize || stickIndex < views.PageSize {
		t.Fatal("fixture needs coin on first page and stick on second page")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	diagnostics := &reloadLog{
		published: make(chan struct{}, 4),
		rejected:  make(chan struct{}, 4),
	}
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, r *telegram.Router) {
		defer cancel()

		// Consume the startup rescan before waiting for the deletion publication.
		waitReload(t, diagnostics.published)

		r.Handle(ctx, message(1, -1, 1, "/admin add", 2))
		r.Handle(ctx, message(2, -1, 1, "/items", 0))
		menuID := len(fake.views)
		menu := waitMenu(t, fake, -1, menuID, nil)
		coin := menu.Rows[0][coinIndex].Data
		next := menu.Rows[1][0].Data
		before := fake.editCount()
		r.Handle(ctx, callback(90, -1, 2, menuID, coin))
		r.Handle(ctx, callback(91, -1, 3, menuID, coin))
		r.Handle(ctx, callback(92, -2, 1, menuID, coin))
		r.Handle(ctx, callback(93, -1, 1, menuID+1, coin))
		for _, update := range []int64{100, 100, 101} {
			r.Handle(ctx, callback(update, -1, 1, menuID, coin))
			if !strings.Contains(fake.acks[len(fake.acks)-1], "Added 1") {
				t.Error("grant did not confirm committed success")
			}
		}
		if fake.editCount() != before {
			t.Error("grant unnecessarily changed catalog menu")
		}

		if err := os.Remove(filepath.Join(cfg.AssetsPath, "items", "coin.cue")); err != nil {
			t.Error(err)
			return
		}
		waitReload(t, diagnostics.published)
		r.Handle(ctx, callback(100, -1, 1, menuID, coin))
		if !strings.Contains(fake.acks[len(fake.acks)-1], "Added 1") {
			t.Error("committed replay lost after catalog deletion")
		}
		r.Handle(ctx, callback(102, -1, 1, menuID, coin))
		if !strings.Contains(fake.acks[len(fake.acks)-1], "Unable to grant") {
			t.Error("removed item granted through old button")
		}

		// Reload leaves six active items, so navigation clamps to page zero.
		r.Handle(ctx, callback(103, -1, 1, menuID, next))
		menu = waitMenu(t, fake, -1, menuID, func(v core.View) bool {
			return len(v.Rows[0]) == 6 && !strings.Contains(textOf(v), "Coin")
		})
		stick := menu.Rows[0][stickIndex-1].Data
		r.Handle(ctx, callback(104, -1, 1, menuID, stick))
		r.Handle(ctx, callback(105, -1, 1, menuID, stick))
		r.Handle(ctx, callback(105, -1, 1, menuID, stick))
		r.Handle(ctx, callback(106, -1, 1, menuID, coin))
		if !strings.Contains(fake.acks[len(fake.acks)-1], "unavailable") {
			t.Error("stale item button accepted after navigation")
		}

		r.Handle(ctx, message(107, -2, 1, "/items", 0))
		menu = waitMenu(t, fake, -2, len(fake.views), nil)
		r.Handle(ctx, callback(108, -2, 1, len(fake.views), menu.Rows[0][stickIndex-1].Data))
	}
	deps := defaults()
	deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
		return fake, nil
	}
	application, err := NewWith(cfg, slog.New(diagnostics), deps)
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

	characters, err := db.LoadCharacters(context.Background())
	if err != nil || len(characters) != 2 {
		t.Fatalf("expected only owner characters in two groups: %v %v", characters, err)
	}
	for _, character := range characters {
		if character.User != 1 {
			t.Fatal("button created another user's character")
		}
	}

	entries, err := db.LoadInventory(context.Background())
	if err != nil || len(entries) != 4 {
		t.Fatalf("expected one stack and three individuals: %v %v", entries, err)
	}
	var coins, sticks, foreignSticks int64
	for _, entry := range entries {
		switch {
		case entry.Group == -1 && entry.Snapshot.CatalogID == "coin":
			coins += entry.Quantity
		case entry.Group == -1 && entry.Snapshot.CatalogID == "stick":
			sticks += entry.Quantity
		case entry.Group == -2 && entry.Snapshot.CatalogID == "stick":
			foreignSticks += entry.Quantity
		default:
			t.Errorf("unexpected grant: %+v", entry)
		}
	}
	if coins != 2 || sticks != 2 || foreignSticks != 1 {
		t.Fatalf("wrong grants: coins=%d sticks=%d foreign sticks=%d", coins, sticks, foreignSticks)
	}
}
