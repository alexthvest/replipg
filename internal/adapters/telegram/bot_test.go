package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func TestTelegramWireEntitiesAndEmptyKeyboard(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}

		if r.MultipartForm != nil {
			defer func() {
				_ = r.MultipartForm.RemoveAll()
			}()
		}

		if r.FormValue("parse_mode") != "" {
			t.Error("user text sent through formatting parser")
		}

		if strings.HasSuffix(r.URL.Path, "answerCallbackQuery") {
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
			return
		}

		if r.FormValue("text") != "🎲 `name`" {
			t.Error("literal text changed")
		}

		var spans []models.MessageEntity
		if err := json.Unmarshal([]byte(r.FormValue("entities")), &spans); err != nil ||
			len(spans) != 1 ||
			spans[0].Offset != 3 ||
			spans[0].Length != 6 {
			t.Errorf("wire entities: %+v %v", spans, err)
		}

		var markup struct {
			Rows [][]models.InlineKeyboardButton `json:"inline_keyboard"`
		}
		if err := json.Unmarshal([]byte(r.FormValue("reply_markup")), &markup); err != nil || markup.Rows == nil {
			t.Errorf("keyboard must encode an array: %s %v", r.FormValue("reply_markup"), err)
		}

		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":42,"date":1,"chat":{"id":-1,"type":"group"}}}`)
	}))
	defer server.Close()

	client, err := newClient("123:test", bot.WithSkipGetMe(), bot.WithServerURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}

	adapter := &Bot{client: client}
	view := core.View{
		Text: core.Text{
			{Text: "🎲 "},
			{
				Text:  "`name`",
				Style: core.Code,
			},
		},
	}
	id, err := adapter.Send(context.Background(), -1, view)
	if err != nil || id != 42 {
		t.Fatalf("send: %d %v", id, err)
	}

	if err = adapter.Edit(context.Background(), -1, 42, view); err != nil {
		t.Fatal(err)
	}

	if err = adapter.Ack(context.Background(), "callback", ""); err != nil {
		t.Fatal(err)
	}

	if requests != 3 {
		t.Fatalf("wire calls: %d", requests)
	}
}

func TestRateLimitRetriesAreBounded(t *testing.T) {
	calls := 0
	err := retry(context.Background(), func(context.Context) error {
		calls++
		return &bot.TooManyRequestsError{RetryAfter: 0}
	})
	if err == nil || calls != 3 {
		t.Fatalf("retry limit: %d %v", calls, err)
	}
}
