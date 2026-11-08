package app

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	"github.com/alexthvest/replipg/internal/adapters/telegram"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/go-telegram/bot/models"
)

func textOf(view core.View) string {
	var result strings.Builder
	for _, span := range view.Text {
		result.WriteString(span.Text)
	}
	return result.String()
}

func TestInventoryGrantNavigationAndReplayAfterRestart(t *testing.T) {
	cfg := testConfig(t)
	var oldAction string
	var durable []core.ID
	for run := 0; run < 2; run++ {
		ctx, cancel := context.WithCancel(context.Background())
		fake := &fakeBot{}
		fake.script = func(ctx context.Context, r *telegram.Router) {
			defer cancel()

			if run == 0 {
				for i, args := range []string{"coin 0", "coin -1", "coin 1.5", "coin 1001", "missing 1"} {
					r.Handle(ctx, message(int64(i+1), -1, 1, "/give "+args, 2))
				}

				invalid := message(10, -1, 1, "/give coin 1", 2)
				invalid.Message.ReplyToMessage.From.IsBot = true
				r.Handle(ctx, invalid)
				invalid = message(11, -1, 1, "/give coin 1", 2)
				invalid.Message.ReplyToMessage.SenderChat = &models.Chat{ID: -5}
				r.Handle(ctx, invalid)
				r.Handle(ctx, message(12, -1, 3, "/give coin 1", 2))
				r.Handle(ctx, message(13, -1, 1, "/give coin 1", 0))
				fake.sendErr = errors.New("send failed")
				r.Handle(ctx, message(100, -1, 1, "/give coin 5", 2))
				fake.sendErr = nil
				r.Handle(ctx, message(100, -1, 1, "/give coin 5", 2))
				r.Handle(ctx, message(101, -1, 1, "/give stick 12", 2))
				r.Handle(ctx, message(102, -1, 2, "/inventory", 0))
				menu := lastEdit(fake)
				messageID := len(fake.views)
				if len(menu.Rows[0]) != 6 || menu.Text[0].Text != "Inventory" {
					t.Error("invalid full page")
					return
				}

				oldAction = menu.Rows[0][0].Data
				before := len(fake.edits)
				r.Handle(ctx, callback(103, -1, 3, messageID, oldAction))
				r.Handle(ctx, callback(104, -2, 2, messageID, oldAction))
				if len(fake.edits) != before {
					t.Error("foreign menu operated")
				}

				r.Handle(ctx, callback(105, -1, 2, messageID, oldAction))
				inspection := lastEdit(fake)
				if len(inspection.Rows) != 1 || len(inspection.Rows[0]) != 1 {
					t.Error("inspection has mutation actions")
				}

				r.Handle(ctx, callback(106, -1, 2, messageID, inspection.Rows[0][0].Data))
				menu = lastEdit(fake)
				for i := 0; i < 2; i++ {
					next := menu.Rows[1][len(menu.Rows[1])-1].Data
					r.Handle(ctx, callback(int64(107+i), -1, 2, messageID, next))
					menu = lastEdit(fake)
				}

				if len(menu.Rows[0]) != 1 {
					t.Error("last page should contain one entry")
				}

				before = len(fake.edits)
				r.Handle(ctx, callback(110, -1, 2, messageID, oldAction))
				if len(fake.edits) != before {
					t.Error("stale action selected a new item")
				}

				r.Handle(ctx, message(111, -2, 2, "/inventory", 0))
				if !strings.Contains(textOf(fake.views[len(fake.views)-1]), "empty") {
					t.Error("group inventory leaked")
				}
			} else {
				r.Handle(ctx, message(100, -1, 1, "/give coin 5", 2))
				r.Handle(ctx, callback(200, -1, 2, 1, oldAction))
				if len(fake.edits) != 0 {
					t.Error("restart retained old menu")
				}

				r.Handle(ctx, message(201, -1, 2, "/inventory", 0))
				if len(lastEdit(fake).Rows[0]) != 6 {
					t.Error("inventory lost after restart")
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

		db, err := sqlite.Open(context.Background(), cfg.DatabasePath)
		if err != nil {
			t.Fatal(err)
		}

		character, ok, err := db.FindCharacter(context.Background(), -1, 2)
		if err != nil || !ok {
			t.Fatal("recipient character missing")
		}

		entries, err := db.Entries(context.Background(), -1, character.ID)
		if err != nil || len(entries) != 13 {
			t.Fatalf("inventory: %d %v", len(entries), err)
		}

		for i, entry := range entries {
			if run == 0 {
				durable = append(durable, entry.ID)
			} else if entry.ID != durable[i] {
				t.Fatal("durable identity changed")
			}

			if entry.Snapshot.CatalogID == "coin" && entry.Quantity != 5 {
				t.Fatal("replay duplicated ownership")
			}
		}

		characters, err := db.LoadCharacters(context.Background())
		if err != nil || len(characters) != 2 {
			t.Fatalf("invalid commands created characters: %d %v", len(characters), err)
		}

		if err = db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
