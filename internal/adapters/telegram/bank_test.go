package telegram

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestBankTransportUsesNumericDestinationAndLiteralJSON(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":"request","method":"bank.payment.status.public.v1","params":{"requestId":"payment"}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			return
		}

		if r.MultipartForm != nil {
			defer func() {
				_ = r.MultipartForm.RemoveAll()
			}()
		}

		if r.FormValue("chat_id") != "99" || r.FormValue("text") != body ||
			r.FormValue("parse_mode") != "" || r.FormValue("entities") != "" {
			t.Errorf("bank wire request changed: %+v", r.Form)
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":42,"date":1,"chat":{"id":99,"type":"private"}}}`)
	}))
	defer server.Close()

	client, err := newClient("123:test", bot.WithSkipGetMe(), bot.WithServerURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	adapter := &Bot{client: client}
	if err = adapter.SendBank(context.Background(), 99, body); err != nil {
		t.Fatal(err)
	}
}

func TestBankReplyAuthentication(t *testing.T) {
	calls := 0
	handler := TrustedBankHandler(99, func(context.Context, string) error {
		calls++
		return nil
	})
	valid := func() *models.Update {
		return &models.Update{
			Message: &models.Message{
				ID: 1,
				From: &models.User{
					ID:    99,
					IsBot: true,
				},
				Chat: models.Chat{
					ID:   99,
					Type: models.ChatTypePrivate,
				},
				Text: `{"jsonrpc":"2.0","id":"known","result":{"data":{"requestId":"payment","state":"pending"}}}`,
			},
		}
	}
	for _, change := range []func(*models.Update){
		func(u *models.Update) {
			u.Message.From.ID = 98
		},
		func(u *models.Update) {
			u.Message.Chat.ID = 98
		},
		func(u *models.Update) {
			u.Message.From.IsBot = false
		},
		func(u *models.Update) {
			u.Message.Chat.Type = models.ChatTypeGroup
		},
		func(u *models.Update) {
			u.Message.EditDate = 1
		},
		func(u *models.Update) {
			u.Message.IsAutomaticForward = true
		},
		func(u *models.Update) {
			u.Message.ForwardOrigin = &models.MessageOrigin{}
		},
		func(u *models.Update) {
			u.EditedMessage = u.Message
			u.Message = nil
		},
		func(u *models.Update) {
			u.Message.Text = "invalid JSON"
		},
		func(u *models.Update) {
			u.Message.Text = `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"Parse error"}}`
		},
	} {
		update := valid()
		change(update)
		handler(context.Background(), update)
	}
	if calls != 0 {
		t.Fatal("untrusted or malformed reply reached settlement")
	}
	if !handler(context.Background(), valid()) || calls != 1 {
		t.Fatal("trusted original reply was ignored")
	}
}

func TestRouterDispatchesBankRepliesWithoutPrivateGameplay(t *testing.T) {
	transport := &fakeTransport{}
	router, err := NewRouter(123, "game", transport, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	gameplay := 0
	if err = router.Command("shop", func(context.Context, core.Scope, Input) (core.View, error) {
		gameplay++
		return core.View{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	bankCalls := 0
	if err = router.OnBankReply(TrustedBankHandler(99, func(context.Context, string) error {
		bankCalls++
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	router.Handle(context.Background(), &models.Update{
		Message: &models.Message{
			Chat: models.Chat{
				ID:   99,
				Type: models.ChatTypePrivate,
			},
			From: &models.User{
				ID:    99,
				IsBot: true,
			},
			Text: `{"jsonrpc":"2.0","id":"known","result":{"data":{"requestId":"payment","state":"pending"}}}`,
		},
	})
	router.Handle(context.Background(), &models.Update{
		Message: &models.Message{
			Chat: models.Chat{
				ID:   42,
				Type: models.ChatTypePrivate,
			},
			From: &models.User{ID: 42},
			Text: "/shop",
		},
	})
	if bankCalls != 1 || gameplay != 0 || transport.sends != 0 || transport.edits != 0 {
		t.Fatal("bank integration enabled private gameplay or emitted a private response")
	}
}
