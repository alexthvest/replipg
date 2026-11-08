package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/content"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/go-telegram/bot/models"
)

type fakeBot struct {
	script    func(context.Context, *telegram.Router)
	mu        sync.Mutex
	views     []core.View
	sendErr   error
	edits     []core.View
	acks      []string
	editErr   error
	delivered map[menuAddress]core.View
}

type menuAddress struct {
	group   core.GroupID
	message int
}

func (f *fakeBot) Identity() (int64, string) {
	return 123, "replipg_bot"
}

func (f *fakeBot) Close() error {
	return nil
}

func (f *fakeBot) Start(intake, delivery context.Context, r *telegram.Router) {
	if f.script != nil {
		f.script(delivery, r)
	}

	<-intake.Done()
}

func (f *fakeBot) Send(_ context.Context, _ core.GroupID, v core.View) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.views = append(f.views, v)
	return len(f.views), f.sendErr
}

func (f *fakeBot) Edit(_ context.Context, group core.GroupID, message int, view core.View) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.edits = append(f.edits, view)
	if f.delivered == nil {
		f.delivered = map[menuAddress]core.View{}
	}
	if f.editErr == nil {
		f.delivered[menuAddress{
			group:   group,
			message: message,
		}] = view
	}
	return f.editErr
}

func (f *fakeBot) editCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.edits)
}

func (f *fakeBot) setEditError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.editErr = err
}

func waitMenu(
	t *testing.T,
	f *fakeBot,
	group core.GroupID,
	message int,
	ready func(core.View) bool,
) core.View {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		f.mu.Lock()
		view, ok := f.delivered[menuAddress{
			group:   group,
			message: message,
		}]
		f.mu.Unlock()
		if ok && (ready == nil || ready(view)) {
			return view
		}

		select {
		case <-deadline.C:
			t.Fatalf("menu %d/%d did not reach expected view; last: %s", group, message, textOf(view))
		case <-tick.C:
		}
	}
}

func changedMenu(t *testing.T, f *fakeBot, group core.GroupID, message int, previous core.View) core.View {
	t.Helper()
	return waitMenu(t, f, group, message, func(view core.View) bool {
		return !reflect.DeepEqual(view, previous)
	})
}

func (f *fakeBot) Ack(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.acks = append(f.acks, text)
	return nil
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	path, err := filepath.Abs("../../assets")
	if err != nil {
		t.Fatal(err)
	}
	return config.Config{
		Token:           "123:secret",
		OwnerID:         1,
		DatabasePath:    filepath.Join(t.TempDir(), "game.db"),
		AssetsPath:      path,
		AssetDebounce:   time.Millisecond,
		MenuTTL:         time.Minute,
		ShutdownTimeout: time.Second,
	}
}

func logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func message(id int64, group int64, actor int64, text string, recipient int64) *models.Update {
	m := &models.Message{
		ID:   int(id),
		Date: 1,
		Chat: models.Chat{
			ID:   group,
			Type: models.ChatTypeSupergroup,
		},
		From: &models.User{
			ID:        actor,
			FirstName: "Player",
		},
		Text: text,
	}
	if recipient > 0 {
		m.ReplyToMessage = &models.Message{
			ID:   100,
			Date: 1,
			Chat: m.Chat,
			From: &models.User{
				ID:        recipient,
				FirstName: "Recipient",
			},
		}
	}
	return &models.Update{
		ID:      id,
		Message: m,
	}
}

func TestStartupUnwindsStorage(t *testing.T) {
	for _, stage := range []string{"assets", "network"} {
		t.Run(stage, func(t *testing.T) {
			cfg := testConfig(t)
			deps := defaults()
			var opened *sqlite.Database
			deps.Open = func(ctx context.Context, path string) (*sqlite.Database, error) {
				var err error
				opened, err = sqlite.Open(ctx, path)
				return opened, err
			}
			if stage == "assets" {
				deps.Load = func(string) (*content.Bundle, error) {
					return nil, errors.New("invalid assets")
				}
			} else {
				deps.Connect = func(context.Context, string, *slog.Logger) (Poller, error) {
					return nil, errors.New("network unavailable")
				}
			}

			app, err := NewWith(cfg, logger(), deps)
			if err != nil {
				t.Fatal(err)
			}

			if err = app.Run(context.Background()); err == nil {
				t.Fatal("startup succeeded")
			}

			if _, err = opened.Group(context.Background(), -1); err == nil {
				t.Fatal("storage left open")
			}
		})
	}

	if _, err := NewWith(testConfig(t), logger(), Dependencies{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}

func TestGroupCommandsPersistAndIsolate(t *testing.T) {
	cfg := testConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fake := &fakeBot{}
	fake.script = func(ctx context.Context, r *telegram.Router) {
		for _, u := range []*models.Update{
			message(1, -1, 2, "/admin add", 3),
			message(2, -1, 1, "/admin add", 2),
			message(3, -2, 1, "/ping", 0),
			message(4, -1, 1, "/admin remove", 2),
			message(5, -2, 1, "/admin add", 3),
		} {
			r.Handle(ctx, u)
		}

		cancel()
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

	if len(a.Admins) != 0 || len(b.Admins) != 1 || b.Admins[0] != 3 {
		t.Fatalf("group permissions: %+v %+v", a, b)
	}

	if len(fake.views) != 5 || fake.views[2].Text[0].Text != "Pong!" {
		t.Fatalf("responses: %+v", fake.views)
	}
}
