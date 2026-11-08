package app

import (
	"context"
	"log/slog"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
)

func TestCharacterCommandsAndRestart(t *testing.T) {
	cfg := testConfig(t)
	for run := 0; run < 2; run++ {
		ctx, cancel := context.WithCancel(context.Background())
		fake := &fakeBot{}
		fake.script = func(ctx context.Context, r *telegram.Router) {
			defer cancel()

			if run == 0 {
				r.Handle(ctx, message(1, -1, 1, "ordinary text", 0))
				r.Handle(ctx, message(2, -1, 1, "/rename   ", 0))
				r.Handle(ctx, message(3, -1, 2, "/settings", 0))
				r.Handle(ctx, message(4, -1, 1, "/character", 0))
				r.Handle(ctx, message(5, -1, 1, "/rename   🎲`[Имя]   ", 0))
				r.Handle(ctx, message(6, -2, 1, "/character", 0))
			} else {
				r.Handle(ctx, message(7, -1, 1, "/character", 0))
				if fake.views[0].Text[2].Text != "🎲`[Имя]" {
					t.Error("saved name lost after restart")
				}
			}
		}
		deps := defaults()
		deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
			return fake, nil
		}
		app, err := NewWith(cfg, logger(), deps)
		if err != nil {
			t.Fatal(err)
		}

		if err = app.Run(ctx); err != nil {
			t.Fatal(err)
		}
	}

	db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = db.Close()
	}()

	all, err := db.LoadCharacters(context.Background())
	if err != nil || len(all) != 2 {
		t.Fatalf("unintended character creation: %+v %v", all, err)
	}

	for _, c := range all {
		if c.User != 1 {
			t.Fatal("settings created character")
		}

		if c.Group == -1 && c.Name != "🎲`[Имя]" {
			t.Fatal(c)
		}

		if c.Group == -2 && c.Name != "Player" {
			t.Fatal(c)
		}
	}
}
