package telegram

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type fakeTransport struct {
	sends int
	edits int
	acks  int
	view  core.View
}

func (f *fakeTransport) Send(_ context.Context, _ core.GroupID, v core.View) (int, error) {
	f.sends++
	f.view = v
	return 100, nil
}

func (f *fakeTransport) Edit(_ context.Context, _ core.GroupID, _ int, v core.View) error {
	f.edits++
	f.view = v
	return nil
}

func (f *fakeTransport) Ack(context.Context, string, string) error {
	f.acks++
	return nil
}

func TestRouterRejectsUnsupportedUpdates(t *testing.T) {
	ctx := context.Background()
	transport := &fakeTransport{}
	r, err := NewRouter(123, "replipg_bot", transport, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	calls := 0
	if err = r.Command("ping", func(_ context.Context, s core.Scope, i Input) (core.View, error) {
		calls++
		if s.Group != -1 || s.Actor != 2 || s.BotID != 123 {
			t.Fatal(s)
		}
		return core.View{Text: core.Literal("Pong!")}, nil
	}); err != nil {
		t.Fatal(err)
	}

	message := func() *models.Message {
		return &models.Message{
			ID:   10,
			Date: 1,
			Chat: models.Chat{
				ID:   -1,
				Type: models.ChatTypeGroup,
			},
			From: &models.User{ID: 2},
			Text: "/ping",
		}
	}

	for _, change := range []func(*models.Message){
		func(m *models.Message) {
			m.Chat.Type = models.ChatTypePrivate
		},
		func(m *models.Message) {
			m.Chat.Type = models.ChatTypeChannel
		},
		func(m *models.Message) {
			m.From = nil
		},
		func(m *models.Message) {
			m.From.IsBot = true
		},
		func(m *models.Message) {
			m.SenderChat = &models.Chat{ID: -5}
		},
		func(m *models.Message) {
			m.Text = "/ping@other_bot"
		},
		func(m *models.Message) {
			m.EditDate = 2
		},
	} {
		m := message()
		change(m)
		r.Handle(ctx, &models.Update{Message: m})
	}

	r.Handle(ctx, &models.Update{EditedMessage: message()})
	r.Handle(ctx, &models.Update{
		CallbackQuery: &models.CallbackQuery{
			From:    models.User{ID: 2},
			Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeInaccessibleMessage},
		},
	})
	if calls != 0 || transport.sends != 0 || transport.edits != 0 {
		t.Fatal("unsupported update handled")
	}

	r.Handle(ctx, &models.Update{Message: message()})
	m := message()
	m.Text = "/ping@RepliPG_Bot"
	r.Handle(ctx, &models.Update{Message: m})
	if calls != 2 || transport.sends != 2 {
		t.Fatalf("valid updates: %d %d", calls, transport.sends)
	}
}

func TestEntitiesUseUTF16AndLiteralText(t *testing.T) {
	text, entities, err := entities(core.Text{
		{Text: "🎲 "},
		{
			Text:  "A`[Б]",
			Style: core.Code,
		},
		{
			Text:  " menu",
			Style: core.Bold,
		},
	})
	if err != nil ||
		text != "🎲 A`[Б] menu" ||
		len(entities) != 2 ||
		entities[0].Offset != 3 ||
		entities[0].Length != 5 ||
		entities[1].Offset != 8 {
		t.Fatalf("entities: %s %+v %v", text, entities, err)
	}

	if _, _, err = entitiesForLimit(); err == nil {
		t.Fatal("oversized message accepted")
	}

	if _, err = keyboard([][]core.Button{
		{
			{
				Caption: "x",
				Data:    strings.Repeat("a", 65),
			},
		},
	}); err == nil {
		t.Fatal("oversized callback accepted")
	}
}

func entitiesForLimit() (string, []models.MessageEntity, error) {
	return entities(core.Literal(strings.Repeat("🎲", 2049)))
}

func TestRetryCancellationAndPermanentFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := retry(ctx, func(context.Context) error {
		calls++
		cancel()
		return &bot.TooManyRequestsError{RetryAfter: 100}
	})
	if err == nil || calls != 1 {
		t.Fatalf("retry cancellation: %v %d", err, calls)
	}

	calls = 0
	err = retry(context.Background(), func(context.Context) error {
		calls++
		return bot.ErrorForbidden
	})
	if err == nil || calls != 1 {
		t.Fatalf("permanent failure: %v %d", err, calls)
	}
}
