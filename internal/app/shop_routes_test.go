package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/go-telegram/bot/models"
)

func TestShopOpeningValidationOwnershipAndDisabledBank(t *testing.T) {
	cfg := testConfig(t)
	cfg.AssetsPath = appAssets(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeBot{}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 3, "/shop extra", 0))
		private := message(2, 3, 3, "/shop", 0)
		private.Message.Chat.Type = models.ChatTypePrivate
		router.Handle(ctx, private)
		channel := message(3, -2, 3, "/shop", 0)
		channel.Message.Chat.Type = models.ChatTypeChannel
		router.Handle(ctx, channel)
		if len(fake.views) != 1 || !strings.Contains(textOf(fake.views[0]), "Use /shop") {
			t.Fatal("malformed or unsupported shop route created a menu")
		}

		router.Handle(ctx, message(4, -1, 2, "/shop", 0))
		menuID := len(fake.views)
		menu := waitMenu(t, fake, -1, menuID, nil)
		if !strings.Contains(textOf(menu), "No matching") || !strings.Contains(textOf(menu), "unavailable") ||
			!strings.HasSuffix(textOf(menu), "\n\nPage 1/1") {
			t.Fatal("empty disabled shop did not render availability")
		}
		orders := buttonData(t, menu, "My orders")
		router.Handle(ctx, callback(5, -1, 3, menuID, orders))
		router.Handle(ctx, callback(6, -2, 2, menuID, orders))
		if !reflect.DeepEqual(menu, waitMenu(t, fake, -1, menuID, nil)) {
			t.Fatal("foreign player or group operated a shop control")
		}
		if hasShopButton(menu, "Buy") || hasShopButton(menu, "Sell") {
			t.Fatal("empty catalog exposed item transaction controls")
		}
		orders = buttonData(t, menu, "My orders")
		router.Handle(ctx, callback(8, -1, 2, menuID, orders))
		waitMenu(t, fake, -1, menuID, func(view core.View) bool {
			return strings.Contains(textOf(view), "no orders")
		})
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
	if err != nil || len(characters) != 1 || characters[0].User != 2 || characters[0].Group != -1 {
		t.Fatalf("shop opening created wrong characters: %+v %v", characters, err)
	}
}

func TestShopSellsRetiredSavedSnapshots(t *testing.T) {
	cfg := testConfig(t)
	cfg.BankID = 900
	cfg.TreasuryID = 2
	cfg.AssetsPath = acceptanceShopAssets(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &shopTestBot{
		fakeBot:  &fakeBot{},
		requests: make(chan bankEnvelope, 16),
	}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give bread 3", 2))
		router.Handle(ctx, message(2, -1, 2, "/shop", 0))
		menuID := len(fake.views)
		buy := waitMenu(t, fake.fakeBot, -1, menuID, nil)
		if !strings.Contains(textOf(buy), "Bread") {
			t.Fatal("active purchase offer missing before retirement")
		}
		router.Handle(ctx, callback(3, -1, 2, menuID, namedItemButton(t, buy, "Bread")))
		changedMenu(t, fake.fakeBot, -1, menuID, buy)
		if err := os.WriteFile(filepath.Join(cfg.AssetsPath, "shop", "bread.cue"), []byte("package shop\nbread: {mode: \"stackable\", sellPrice: \"5\"}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(cfg.AssetsPath, "items", "bread.cue")); err != nil {
			t.Fatal(err)
		}
		card := waitMenu(t, fake.fakeBot, -1, menuID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Bread") && !hasShopButton(view, "Buy") && hasShopButton(view, "Sell")
		})
		router.Handle(ctx, callback(4, -1, 2, menuID, buttonData(t, card, "Sell")))
		review := changedMenu(t, fake.fakeBot, -1, menuID, card)
		router.Handle(ctx, callback(5, -1, 2, menuID, buttonData(t, review, "+")))
		review = changedMenu(t, fake.fakeBot, -1, menuID, review)
		router.Handle(ctx, callback(6, -1, 2, menuID, buttonData(t, review, "+")))
		waitMenu(t, fake.fakeBot, -1, menuID, func(view core.View) bool {
			text := textOf(view)
			return strings.Contains(text, "Bread") && strings.Contains(text, "Quantity: 3") && strings.Contains(text, "Total: 15")
		})
		select {
		case request := <-fake.requests:
			t.Fatalf("review sent money without confirmation: %+v", request)
		default:
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
}

func TestShopPriceReloadRequiresNewConfirmation(t *testing.T) {
	cfg := testConfig(t)
	cfg.BankID = 900
	cfg.TreasuryID = 2
	cfg.AssetsPath = acceptanceShopAssets(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &shopTestBot{
		fakeBot:  &fakeBot{},
		requests: make(chan bankEnvelope, 16),
	}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 2, "/shop", 0))
		menu := waitMenu(t, fake.fakeBot, -1, 1, nil)
		router.Handle(ctx, callback(2, -1, 2, 1, buttonData(t, menu, "1")))
		card := changedMenu(t, fake.fakeBot, -1, 1, menu)
		router.Handle(ctx, callback(50, -1, 2, 1, buttonData(t, card, "Buy")))
		review := changedMenu(t, fake.fakeBot, -1, 1, card)
		oldConfirm := buttonData(t, review, "Confirm")
		if !strings.Contains(textOf(review), "Buy") || !strings.Contains(textOf(review), "Total: 10") {
			t.Fatal("review omitted direction or exact total")
		}
		path := filepath.Join(cfg.AssetsPath, "shop", "bread.cue")
		if err := os.WriteFile(path, []byte("package shop\nbread: {mode: \"stackable\", buyPrice: \"11\", sellPrice: \"5\"}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		updated := waitMenu(t, fake.fakeBot, -1, 1, func(view core.View) bool {
			return strings.Contains(textOf(view), "Total: 11")
		})
		router.Handle(ctx, callback(3, -1, 2, 1, oldConfirm))
		if !reflect.DeepEqual(updated, waitMenu(t, fake.fakeBot, -1, 1, nil)) {
			t.Fatal("stale confirmation changed the current review")
		}
		select {
		case request := <-fake.requests:
			t.Fatalf("stale quote sent a bank request: %+v", request)
		default:
		}
		router.Handle(ctx, callback(4, -1, 2, 1, buttonData(t, updated, "Confirm")))
		request := nextBankRequest(t, fake)
		if !strings.Contains(string(request.Params), `"amount":"11"`) {
			t.Fatalf("updated confirmation did not freeze new price: %s", request.Params)
		}
		order := waitMenu(t, fake.fakeBot, -1, 1, func(view core.View) bool {
			return strings.Contains(textOf(view), "Order")
		})
		router.Handle(ctx, callback(5, -1, 2, 1, buttonData(t, order, "Refresh")))
		router.Handle(ctx, callback(6, -1, 2, 1, buttonData(t, order, "Back")))
		orders := waitMenu(t, fake.fakeBot, -1, 1, func(view core.View) bool {
			return strings.HasPrefix(textOf(view), "Shop • My orders\n\n")
		})
		router.Handle(ctx, callback(7, -1, 2, 1, buttonData(t, orders, "Back")))
		catalog := waitMenu(t, fake.fakeBot, -1, 1, func(view core.View) bool {
			return strings.HasPrefix(textOf(view), "Shop\n\n")
		})
		router.Handle(ctx, callback(8, -1, 2, 1, buttonData(t, catalog, "1")))
		newCard := changedMenu(t, fake.fakeBot, -1, 1, catalog)
		router.Handle(ctx, callback(51, -1, 2, 1, buttonData(t, newCard, "Buy")))
		newReview := changedMenu(t, fake.fakeBot, -1, 1, newCard)
		router.Handle(ctx, callback(9, -1, 2, 1, buttonData(t, newReview, "Confirm")))
		second := nextBankRequest(t, fake)
		if second.ID == request.ID {
			t.Fatal("another reviewed purchase reused the preceding accepted confirmation")
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
}
