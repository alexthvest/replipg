package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/groups"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/go-telegram/bot/models"
)

func callback(id int64, group, actor int64, message int, data string) *models.Update {
	return &models.Update{
		ID: id,
		CallbackQuery: &models.CallbackQuery{
			ID: "callback",
			From: models.User{
				ID:        actor,
				FirstName: "Player",
			},
			Data: data,
			Message: models.MaybeInaccessibleMessage{
				Type: models.MaybeInaccessibleMessageTypeMessage,
				Message: &models.Message{
					ID:   message,
					Date: 1,
					Chat: models.Chat{
						ID:   group,
						Type: models.ChatTypeSupergroup,
					},
				},
			},
		},
	}
}

func lastEdit(f *fakeBot) core.View {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.edits[len(f.edits)-1]
}

func TestSettingsSaveEditPermissionsAndRestart(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake := &fakeBot{}
	fake.script = func(ctx context.Context, r *telegram.Router) {
		defer cancel()

		r.Handle(ctx, message(1, -1, 2, "/settings", 0))
		menu := lastEdit(fake)
		if len(menu.Rows) != 2 || menu.Text[0].Style != core.Bold || menu.Text[2].Style != core.Code {
			t.Error("invalid settings view")
			return
		}

		english, russian := menu.Rows[0][0].Data, menu.Rows[1][0].Data
		// Everyone may open the menu, but unknown users and foreign references cannot change it.
		r.Handle(ctx, callback(2, -1, 2, 1, russian))
		r.Handle(ctx, callback(3, -2, 1, 1, russian))
		r.Handle(ctx, callback(4, -1, 1, 999, russian))
		if len(fake.views) != 1 || len(fake.edits) != 1 || len(fake.acks) != 3 {
			t.Error("denied settings changed menu")
			return
		}

		r.Handle(ctx, message(5, -1, 1, "/admin add", 2))
		r.Handle(ctx, callback(6, -1, 2, 1, russian))
		if lastEdit(fake).Text[0].Text != "Настройки" {
			t.Error("Russian selection did not edit same menu")
		}

		edits := len(fake.edits)
		sends := len(fake.views)
		r.Handle(ctx, callback(7, -1, 2, 1, russian))
		if len(fake.edits) != edits || len(fake.views) != sends {
			t.Error("repeated choice produced output")
		}

		r.Handle(ctx, message(8, -1, 1, "/admin remove", 2))
		r.Handle(ctx, callback(9, -1, 2, 1, english))
		if len(fake.edits) != edits {
			t.Error("revoked admin changed language")
		}
		// Edit failure does not roll back the saved language and callbacks send no messages.

		fake.editErr = errors.New("network failure")
		sends = len(fake.views)
		r.Handle(ctx, callback(10, -1, 1, 1, english))
		if len(fake.views) != sends {
			t.Error("language notification posted")
		}

		fake.editErr = nil
		r.Handle(ctx, message(11, -1, 3, "/settings", 0))
		if lastEdit(fake).Text[0].Text != "Settings" {
			t.Error("saved selection lost after failed edit")
		}
		// Concurrent authorized selections are rendered in serialized order.

		var joined sync.WaitGroup
		joined.Add(2)
		go func() {
			defer joined.Done()

			r.Handle(ctx, callback(12, -1, 1, 1, russian))
		}()
		go func() {
			defer joined.Done()

			r.Handle(ctx, callback(13, -1, 1, 1, english))
		}()
		joined.Wait()
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

	a, err := db.Group(context.Background(), -1)
	if err != nil {
		t.Fatal(err)
	}

	b, err := db.Group(context.Background(), -2)
	if err != nil {
		t.Fatal(err)
	}

	if b.Language != "en" {
		t.Fatal("foreign group changed")
	}

	expected := "Settings"
	if a.Language == "ru" {
		expected = "Настройки"
	}

	if lastEdit(fake).Text[0].Text != expected {
		t.Fatal("stale render overwrote saved selection")
	}
}

type failedSettings struct {
	*sqlite.Database
}

func (f failedSettings) SetLanguage(context.Context, core.GroupID, string) (groups.State, error) {
	return groups.State{}, errors.New("commit failed")
}

func TestSettingsCommitFailurePreservesMenu(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	db, err := sqlite.Open(ctx, cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = db.Close()
	}()

	service, err := groups.New(failedSettings{db}, 1)
	if err != nil {
		t.Fatal(err)
	}

	bundle, err := cueassets.Load(cfg.AssetsPath)
	if err != nil {
		t.Fatal(err)
	}

	characterService, err := characters.New(db, core.NewID)
	if err != nil {
		t.Fatal(err)
	}

	modules, err := newModules(service, characterService, bundle, cfg.MenuTTL)
	if err != nil {
		t.Fatal(err)
	}

	registry := runtime.NewRegistry()
	routes, err := groupRoutes(registry, modules)
	if err != nil {
		t.Fatal(err)
	}

	engine, err := runtime.New(registry, 8, cfg.ShutdownTimeout)
	if err != nil {
		t.Fatal(err)
	}

	if err = engine.Start(ctx); err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = engine.Stop(ctx)
	}()

	scope := core.Scope{
		Group: -1,
		Actor: 1,
	}
	opened, err := routes["settings"](ctx, engine, scope, telegram.Input{})
	if err != nil {
		t.Fatal(err)
	}

	bound, err := routes["bind-menu"](ctx, engine, scope, telegram.Input{
		Data:      opened.Menu,
		MessageID: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := routes["menu"](ctx, engine, scope, telegram.Input{
		Data:      bound.Rows[1][0].Data,
		MessageID: 10,
	})
	if err != nil || result.Feedback == "" || len(result.Text) != 0 {
		t.Fatalf("commit failure: %+v %v", result, err)
	}

	state, err := db.Group(ctx, -1)
	if err != nil || state.Language != "en" {
		t.Fatalf("failed commit changed state: %+v %v", state, err)
	}
}
