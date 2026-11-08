package app

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
)

type bindingTestBot struct {
	*fakeBot
	betweenSendAndBind func(context.Context)
}

func (b *bindingTestBot) Send(ctx context.Context, group core.GroupID, view core.View) (int, error) {
	id, err := b.fakeBot.Send(ctx, group, view)
	if err == nil && view.Menu != "" && b.betweenSendAndBind != nil {
		b.betweenSendAndBind(ctx)
	}
	return id, err
}

func TestCommittedGrantsRefreshSubscriptionsAndCloseBindingGap(t *testing.T) {
	cfg := testConfig(t)
	cfg.AssetsPath = appAssets(t)
	diagnostics := &reloadLog{
		published: make(chan struct{}, 32),
		rejected:  make(chan struct{}, 32),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &bindingTestBot{fakeBot: &fakeBot{}}
	fake.script = func(ctx context.Context, router *telegram.Router) {
		defer cancel()
		waitReload(t, diagnostics.published)

		fake.betweenSendAndBind = func(ctx context.Context) {
			fake.betweenSendAndBind = nil
			router.Handle(ctx, message(2, -1, 1, "/give coin 2", 2))
		}
		router.Handle(ctx, message(1, -1, 2, "/inventory", 0))
		first := waitMenu(t, fake.fakeBot, -1, 1, func(view core.View) bool {
			return strings.Contains(textOf(view), "Coin")
		})
		if !strings.Contains(textOf(first), "2") {
			t.Fatal("binding retained the empty pre-send inventory")
		}

		router.Handle(ctx, message(3, -1, 2, "/inventory", 0))
		secondID := len(fake.views)
		second := waitMenu(t, fake.fakeBot, -1, secondID, nil)
		router.Handle(ctx, message(4, -2, 2, "/inventory", 0))
		foreignID := len(fake.views)
		foreign := waitMenu(t, fake.fakeBot, -2, foreignID, nil)
		router.Handle(ctx, message(5, -1, 3, "/inventory", 0))
		otherID := len(fake.views)
		other := waitMenu(t, fake.fakeBot, -1, otherID, nil)

		router.Handle(ctx, message(6, -1, 1, "/give coin 3", 2))
		first = changedMenu(t, fake.fakeBot, -1, 1, first)
		second = changedMenu(t, fake.fakeBot, -1, secondID, second)
		if !strings.Contains(textOf(first), "5") || !strings.Contains(textOf(second), "5") {
			t.Fatal("not all current-group inventories refreshed committed quantity")
		}
		if !reflect.DeepEqual(foreign, waitMenu(t, fake.fakeBot, -2, foreignID, nil)) ||
			!reflect.DeepEqual(other, waitMenu(t, fake.fakeBot, -1, otherID, nil)) {
			t.Fatal("grant refreshed another player's or group's inventory")
		}

		// An unrelated grant must preserve an existing inspection while another
		// subscription to the same inventory updates its list.
		router.Handle(ctx, callback(7, -1, 2, 1, first.Rows[0][0].Data))
		inspection := changedMenu(t, fake.fakeBot, -1, 1, first)
		router.Handle(ctx, message(8, -1, 1, "/give bread 1", 2))
		waitMenu(t, fake.fakeBot, -1, secondID, func(view core.View) bool {
			return strings.Contains(textOf(view), "Bread")
		})
		if !reflect.DeepEqual(inspection, waitMenu(t, fake.fakeBot, -1, 1, nil)) {
			t.Fatal("background grant replaced the current inspection")
		}

		router.Handle(ctx, message(9, -1, 2, "/rename New Name", 0))
		waitMenu(t, fake.fakeBot, -1, secondID, func(view core.View) bool {
			return strings.Contains(textOf(view), "New Name")
		})
		var settingsIDs, catalogIDs []int
		var settings core.View
		for i := 0; i < 2; i++ {
			router.Handle(ctx, message(int64(20+i), -1, int64(2+i), "/settings", 0))
			settingsIDs = append(settingsIDs, len(fake.views))
			settings = waitMenu(t, fake.fakeBot, -1, len(fake.views), nil)
			router.Handle(ctx, message(int64(30+i), -1, 1, "/items", 0))
			catalogIDs = append(catalogIDs, len(fake.views))
			waitMenu(t, fake.fakeBot, -1, len(fake.views), nil)
		}
		router.Handle(ctx, message(40, -2, 1, "/items", 0))
		foreignCatalogID := len(fake.views)
		foreignCatalog := waitMenu(t, fake.fakeBot, -2, foreignCatalogID, nil)
		router.Handle(ctx, callback(41, -1, 1, settingsIDs[1], settings.Rows[1][0].Data))
		for _, id := range settingsIDs {
			waitMenu(t, fake.fakeBot, -1, id, func(view core.View) bool {
				return view.Text[0].Text == "Настройки"
			})
		}
		for _, id := range catalogIDs {
			waitMenu(t, fake.fakeBot, -1, id, func(view core.View) bool {
				return view.Text[0].Text == "Предметы"
			})
		}
		if !reflect.DeepEqual(foreignCatalog, waitMenu(t, fake.fakeBot, -2, foreignCatalogID, nil)) {
			t.Fatal("language change leaked into another group's catalog")
		}
		waitMenu(t, fake.fakeBot, -1, 1, func(view core.View) bool {
			return strings.Contains(textOf(view), "Монета")
		})

		writeCoinText(t, cfg.AssetsPath, true)
		waitReload(t, diagnostics.published)
		for _, id := range catalogIDs {
			waitMenu(t, fake.fakeBot, -1, id, func(view core.View) bool {
				return strings.Contains(textOf(view), "Новая монета")
			})
		}
		if strings.Contains(textOf(waitMenu(t, fake.fakeBot, -1, 1, nil)), "Новая монета") {
			t.Fatal("content publication replaced saved owned-item text")
		}
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
}
