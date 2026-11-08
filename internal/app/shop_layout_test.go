package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/localization"
)

func TestShopReferenceLayoutAcrossBuySellAndOrders(t *testing.T) {
	for _, language := range []string{"en", "ru"} {
		t.Run(language, func(t *testing.T) {
			cfg := testConfig(t)
			cfg.BankID = 900
			cfg.TreasuryID = 2
			cfg.AssetsPath = appAssets(t)
			bundle, err := cueassets.Load(cfg.AssetsPath)
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{"bread", "coin", "fumo", "iron_sword", "ruby", "silver_ring", "stick"}
			dir := filepath.Join(cfg.AssetsPath, "shop")
			if err = os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			schema, err := os.ReadFile("../../assets/shop/schema.cue")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(dir, "schema.cue"), schema, 0600); err != nil {
				t.Fatal(err)
			}
			for _, id := range ids {
				definition, found := bundle.Item(id)
				if !found {
					t.Fatalf("missing fixture item %s", id)
				}
				data := fmt.Sprintf("package shop\n%s: {mode: %q, buyPrice: \"10\", sellPrice: \"5\"}\n", id, definition.Mode)
				if err = os.WriteFile(filepath.Join(dir, id+".cue"), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			db, err := sqlite.Open(ctx, cfg.DatabasePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = db.SetLanguage(ctx, -1, language); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
			if err = db.Close(); err != nil {
				t.Fatal(err)
			}

			catalog := bundle.Locales()
			fake := &shopTestBot{
				fakeBot:  &fakeBot{},
				requests: make(chan bankEnvelope, 16),
			}
			fake.script = func(ctx context.Context, router *telegram.Router) {
				defer cancel()
				for index, id := range ids {
					quantity := "1"
					if id == "bread" {
						quantity = "3"
					}
					router.Handle(ctx, message(int64(index+1), -1, 1, "/give "+id+" "+quantity, 2))
				}
				router.Handle(ctx, message(20, -1, 2, "/shop", 0))
				menuID := len(fake.views)
				menu := waitMenu(t, fake.fakeBot, -1, menuID, nil)
				assertReferenceShopBrowse(t, menu, catalog, language, 6, "1/2")
				update := int64(21)
				click := func(view core.View, caption string) core.View {
					t.Helper()
					router.Handle(ctx, callback(update, -1, 2, menuID, buttonData(t, view, caption)))
					update++
					return changedMenu(t, fake.fakeBot, -1, menuID, view)
				}
				menu = click(menu, catalog.Text(language, "menu.next"))
				assertReferenceShopBrowse(t, menu, catalog, language, 1, "2/2")
				individual := click(menu, "1")
				for _, direction := range []string{"buy", "sell"} {
					review := click(individual, catalog.Text(language, "menu.shop."+direction))
					if len(review.Rows) != 2 ||
						strings.Contains(textOf(review), catalog.Text(language, "menu.shop.quantity")+":") {
						t.Fatalf("individual item exposes quantity selection: %+v", review)
					}
					individual = click(review, catalog.Text(language, "menu.back"))
				}
				menu = click(individual, catalog.Text(language, "menu.back"))
				menu = click(menu, catalog.Text(language, "menu.previous"))
				card := click(menu, "1")
				if len(card.Rows) != 2 || len(card.Rows[0]) != 2 || len(card.Rows[1]) != 2 ||
					card.Rows[0][0].Caption != catalog.Text(language, "menu.shop.buy") ||
					card.Rows[0][1].Caption != catalog.Text(language, "menu.shop.sell") ||
					card.Rows[1][0].Caption != catalog.Text(language, "menu.back") ||
					card.Rows[1][1].Caption != catalog.Text(language, "menu.shop.orders") {
					t.Fatalf("item card lost its Buy/Sell and navigation rows: %+v", card.Rows)
				}
				assertPlainShopButtons(t, card)
				sale := click(card, catalog.Text(language, "menu.shop.sell"))
				sale = click(sale, catalog.Text(language, "menu.shop.max"))
				if !strings.Contains(textOf(sale), catalog.Text(language, "menu.shop.quantity")+": 3") {
					t.Fatal("sale Max exceeded the available owned quantity")
				}
				card = click(sale, catalog.Text(language, "menu.back"))
				review := click(card, catalog.Text(language, "menu.shop.buy"))
				if len(review.Rows) != 3 || len(review.Rows[0]) != 4 || len(review.Rows[1]) != 1 || len(review.Rows[2]) != 2 {
					t.Fatalf("review controls do not match the reference: %+v", review.Rows)
				}
				for index, caption := range []string{"1", "−", "+", catalog.Text(language, "menu.shop.max")} {
					if review.Rows[0][index].Caption != caption {
						t.Fatalf("quantity choices not compact or ordered: %+v", review.Rows[0])
					}
				}
				if review.Rows[1][0].Caption != catalog.Text(language, "menu.shop.confirm") ||
					review.Rows[2][0].Caption != catalog.Text(language, "menu.back") || review.Rows[2][1].Caption != catalog.Text(language, "menu.shop.orders") {
					t.Fatal("review lost confirmation or Back/My orders pairing")
				}
				assertPlainShopButtons(t, review)
				review = click(review, catalog.Text(language, "menu.shop.max"))
				if !strings.Contains(textOf(review), catalog.Text(language, "menu.shop.quantity")+": 1000") {
					t.Fatal("purchase Max did not select the supported quantity limit")
				}
				order := click(review, catalog.Text(language, "menu.shop.confirm"))
				if len(order.Rows) != 1 || len(order.Rows[0]) != 2 || order.Rows[0][0].Caption != catalog.Text(language, "menu.shop.refresh") ||
					order.Rows[0][1].Caption != catalog.Text(language, "menu.back") {
					t.Fatalf("order detail lost Refresh/Back pairing: %+v", order.Rows)
				}
				assertPlainShopButtons(t, order)
				router.Handle(ctx, callback(update, -1, 2, menuID, buttonData(t, order, catalog.Text(language, "menu.back"))))
				update++
				orders := waitMenu(t, fake.fakeBot, -1, menuID, func(view core.View) bool {
					return strings.HasPrefix(textOf(view), catalog.Text(language, "menu.shop.title")+" • "+catalog.Text(language, "menu.shop.orders")+"\n\n")
				})
				if len(orders.Rows) != 2 || len(orders.Rows[0]) != 1 || orders.Rows[1][0].Caption != catalog.Text(language, "menu.back") {
					t.Fatalf("order list controls differ from the reference: %+v", orders.Rows)
				}
				quantity, status, contextLabel := false, false, false
				for _, span := range orders.Text {
					quantity = quantity || (span.Style == core.Code && span.Text == "(1000)")
					status = status || (span.Style == core.Italic && strings.TrimSpace(span.Text) != "")
					contextLabel = contextLabel || (span.Style == core.Code && span.Text == catalog.Text(language, "menu.shop.orders"))
				}
				if !quantity || !status || !contextLabel {
					t.Fatal("order list lost saved quantity, secondary status, or styled context")
				}
				menu = click(orders, catalog.Text(language, "menu.back"))
				assertReferenceShopBrowse(t, menu, catalog, language, 6, "1/2")
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
		})
	}
}

func assertReferenceShopBrowse(
	t *testing.T,
	view core.View,
	catalog localization.Catalog,
	language string,
	items int,
	page string,
) {
	t.Helper()
	heading := catalog.Text(language, "menu.shop.title")
	if !strings.HasPrefix(textOf(view), heading+"\n\n") ||
		!strings.HasSuffix(textOf(view), "\n\n"+catalog.Text(language, "menu.inventory.page")+" "+page) {
		t.Fatalf("shop title or page footer does not match the inventory layout: %s", textOf(view))
	}
	if len(view.Rows) != 3 || len(view.Rows[0]) != items || len(view.Rows[1]) != 1 || len(view.Rows[2]) != 1 {
		t.Fatalf("browse controls do not match the reference: %+v", view.Rows)
	}
	for index, button := range view.Rows[0] {
		if button.Caption != strconv.Itoa(index+1) {
			t.Fatalf("numbered selectors do not match the visible page: %+v", view.Rows[0])
		}
	}
	if view.Rows[2][0].Caption != catalog.Text(language, "menu.shop.orders") ||
		hasShopButton(view, catalog.Text(language, "menu.shop.buy")) || hasShopButton(view, catalog.Text(language, "menu.shop.sell")) {
		t.Fatal("catalog must expose My orders and defer Buy/Sell to the selected item")
	}
	assertPlainShopButtons(t, view)
}

func assertPlainShopButtons(t *testing.T, view core.View) {
	t.Helper()
	for _, row := range view.Rows {
		for _, button := range row {
			if button.Style != core.ButtonDefault {
				t.Fatalf("shop button is not plain: %+v", button)
			}
		}
	}
}
