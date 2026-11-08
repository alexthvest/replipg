package app

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	appshop "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/shop"
)

type shutdownTestBot struct {
	*shopTestBot
	bankStarted  chan struct{}
	bankActive   atomic.Bool
	menuActive   atomic.Bool
	closedActive atomic.Bool
	closed       atomic.Bool
}

func (b *shutdownTestBot) SendBank(ctx context.Context, _ int64, _ string) error {
	b.bankActive.Store(true)
	defer b.bankActive.Store(false)
	close(b.bankStarted)
	<-ctx.Done()
	return ctx.Err()
}

func (b *shutdownTestBot) Close() error {
	b.closedActive.Store(b.bankActive.Load() || b.menuActive.Load())
	b.closed.Store(true)
	return nil
}

func TestShutdownJoinsBankAndMenuWorkersBeforeTransportCleanup(t *testing.T) {
	cfg := testConfig(t)
	cfg.BankID = 900
	cfg.TreasuryID = 2
	cfg.AssetsPath = acceptanceShopAssets(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &shutdownTestBot{
		shopTestBot: &shopTestBot{
			fakeBot: &fakeBot{},
		},
		bankStarted: make(chan struct{}),
	}
	menuStarted := make(chan struct{})
	fake.afterEdit = func(ctx context.Context, _ core.GroupID, _ int, view core.View) error {
		if !strings.Contains(textOf(view), "Reserved: 1") {
			return nil
		}
		fake.menuActive.Store(true)
		defer fake.menuActive.Store(false)
		close(menuStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		router.Handle(ctx, message(1, -1, 1, "/give bread 1", 2))
		router.Handle(ctx, message(2, -1, 2, "/inventory", 0))
		inventoryID := len(fake.views)
		inventory := waitMenu(t, fake.fakeBot, -1, inventoryID, nil)
		router.Handle(ctx, callback(3, -1, 2, inventoryID, buttonData(t, inventory, "1")))
		changedMenu(t, fake.fakeBot, -1, inventoryID, inventory)
		router.Handle(ctx, message(4, -1, 2, "/shop", 0))
		shopID := len(fake.views)
		catalog := waitMenu(t, fake.fakeBot, -1, shopID, nil)
		router.Handle(ctx, callback(5, -1, 2, shopID, namedItemButton(t, catalog, "Bread")))
		card := changedMenu(t, fake.fakeBot, -1, shopID, catalog)
		router.Handle(ctx, callback(6, -1, 2, shopID, buttonData(t, card, "Sell")))
		review := changedMenu(t, fake.fakeBot, -1, shopID, card)
		router.Handle(ctx, callback(7, -1, 2, shopID, buttonData(t, review, "Confirm")))
		for _, started := range []chan struct{}{menuStarted, fake.bankStarted} {
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("worker did not begin its in-flight operation")
			}
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
	if !fake.closed.Load() || fake.closedActive.Load() {
		t.Fatal("transport cleanup preceded worker joins")
	}
	db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
	}()
	entries, err := db.LoadInventory(context.Background())
	if err != nil || len(entries) != 1 || entries[0].Quantity != 1 || entries[0].Reserved != 1 {
		t.Fatalf("interrupted payout lost reserved ownership: %+v %v", entries, err)
	}
	err = db.WithinShop(context.Background(), func(tx appshop.Transaction) error {
		orders, err := tx.Orders.List(context.Background(), -1, 2)
		if err != nil {
			return err
		}
		if len(orders) != 1 || orders[0].State != shop.PayoutRequesting {
			t.Fatalf("interrupted payout lost its durable order: %+v", orders)
		}
		requests, err := tx.Requests.Due(context.Background(), time.Now().Add(time.Hour), 10)
		if err != nil {
			return err
		}
		if len(requests) != 1 || !requests[0].Unknown || requests[0].FinancialKey == "" || len(requests[0].Params) == 0 {
			t.Fatalf("interrupted send lost original recovery intent: %+v", requests)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
