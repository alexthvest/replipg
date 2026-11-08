package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
)

type reloadLog struct {
	published chan struct{}
	rejected  chan struct{}
}

func (l *reloadLog) Enabled(context.Context, slog.Level) bool {
	return true
}

func (l *reloadLog) Handle(_ context.Context, r slog.Record) error {
	var out chan struct{}
	switch r.Message {
	case "assets published":
		out = l.published
	case "asset reload rejected":
		out = l.rejected
	}

	if out != nil {
		select {
		case out <- struct{}{}:
		default:
		}
	}
	return nil
}

func (l *reloadLog) WithAttrs([]slog.Attr) slog.Handler {
	return l
}

func (l *reloadLog) WithGroup(string) slog.Handler {
	return l
}

func waitReload(t *testing.T, c <-chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatal("asset publication diagnostic did not arrive")
	}
}

func appAssets(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"items", "locales"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}

		files, err := filepath.Glob(filepath.Join("../../assets", name, "*.cue"))
		if err != nil {
			t.Fatal(err)
		}

		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if name == "locales" && filepath.Base(path) != "schema.cue" {
				var lines []string
				for _, line := range strings.Split(string(data), "\n") {
					if !strings.Contains(line, "\"item.coin.") {
						lines = append(lines, line)
					}
				}

				data = []byte(strings.Join(lines, "\n"))
			}

			if err = os.WriteFile(filepath.Join(root, name, filepath.Base(path)), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}

	writeCoinText(t, root, false)
	return root
}

func writeCoinText(t *testing.T, root string, changed bool) {
	t.Helper()
	english, russian := "Coin", "Монета"
	if changed {
		english = "New coin"
		russian = "Новая монета"
	}

	data := "package locales\nen: {\"item.coin.name\": \"" + english + "\", \"item.coin.description\": \"A simple coin.\"}\nru: {\"item.coin.name\": \"" + russian + "\", \"item.coin.description\": \"Обычная монета.\"}\n"
	temp := filepath.Join(root, "locales", "coin_text.tmp")
	if err := os.WriteFile(temp, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(temp, filepath.Join(root, "locales", "coin_text.cue")); err != nil {
		t.Fatal(err)
	}
}

func TestLiveReloadDeletionAndSavedBilingualItems(t *testing.T) {
	cfg := testConfig(t)
	cfg.AssetsPath = appAssets(t)
	cfg.AssetDebounce = 10 * time.Millisecond
	var previous []core.ID
	for run := 0; run < 2; run++ {
		log := &reloadLog{
			published: make(chan struct{}, 32),
			rejected:  make(chan struct{}, 32),
		}
		ctx, cancel := context.WithCancel(context.Background())
		fake := &fakeBot{}
		fake.script = func(ctx context.Context, r *telegram.Router) {
			defer cancel()

			waitReload(t, log.published)
			if run == 0 {
				r.Handle(ctx, message(100, -1, 1, "/give coin 5", 2))
				r.Handle(ctx, message(101, -1, 1, "/settings", 0))
				settingsID := len(fake.views)
				menu := lastEdit(fake)
				r.Handle(ctx, callback(102, -1, 1, settingsID, menu.Rows[1][0].Data))
				writeCoinText(t, cfg.AssetsPath, true)
				waitReload(t, log.published)
				r.Handle(ctx, message(103, -1, 1, "/give coin 2", 2))
				invalid := filepath.Join(cfg.AssetsPath, "items", "invalid.cue")
				if err := os.WriteFile(invalid, []byte("invalid syntax {{{"), 0600); err != nil {
					t.Fatal(err)
				}

				waitReload(t, log.rejected)
				r.Handle(ctx, message(104, -1, 1, "/give coin 1", 2))
				if err := os.Remove(invalid); err != nil {
					t.Fatal(err)
				}

				waitReload(t, log.published)
				if err := os.Remove(filepath.Join(cfg.AssetsPath, "items", "coin.cue")); err != nil {
					t.Fatal(err)
				}

				waitReload(t, log.published)
			}

			r.Handle(ctx, message(100, -1, 1, "/give coin 5", 2))
			if !strings.Contains(textOf(fake.views[len(fake.views)-1]), "Монета") {
				t.Error("replay confirmation lost original Russian item name")
			}

			r.Handle(ctx, message(105, -1, 1, "/give coin 1", 2))
			r.Handle(ctx, message(106, -1, 2, "/inventory", 0))
			inventoryID := len(fake.views)
			menu := lastEdit(fake)
			text := textOf(menu)
			if !strings.Contains(text, "Новая монета") ||
				!strings.Contains(text, "Монета") ||
				len(menu.Rows[0]) != 2 {
				t.Errorf("saved versions missing: %s", text)
				return
			}

			for i := 0; i < 2; i++ {
				r.Handle(ctx, callback(int64(110+i*2), -1, 2, inventoryID, menu.Rows[0][i].Data))
				inspection := lastEdit(fake)
				if !strings.Contains(textOf(inspection), "Обычный") {
					t.Error("live localized rarity missing")
				}

				r.Handle(ctx, callback(int64(111+i*2), -1, 2, inventoryID, inspection.Rows[0][0].Data))
				menu = lastEdit(fake)
			}

			r.Handle(ctx, message(120, -2, 2, "/ping", 0))
			if textOf(fake.views[len(fake.views)-1]) != "Pong!" {
				t.Error("group language leaked")
			}
		}
		deps := defaults()
		deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
			return fake, nil
		}
		application, err := NewWith(cfg, slog.New(log), deps)
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

		c, ok, err := db.FindCharacter(context.Background(), -1, 2)
		if err != nil || !ok {
			t.Fatal("missing recipient")
		}

		entries, err := db.Entries(context.Background(), -1, c.ID)
		if err != nil || len(entries) != 2 {
			t.Fatalf("saved entries: %+v %v", entries, err)
		}

		for i, entry := range entries {
			if run == 0 {
				previous = append(previous, entry.ID)
			} else if previous[i] != entry.ID {
				t.Error("snapshot identity lost")
			}

			if entry.Quantity != 3 && entry.Quantity != 5 {
				t.Error("reload/replay changed quantities")
			}
		}

		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
