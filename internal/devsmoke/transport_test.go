package devsmoke

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/observability"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/telegram"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func apiResponse(body string) testsupport.TelegramResponse {
	return testsupport.TelegramResponse{Body: []byte(body)}
}

func TestSmokeProductionTransport(t *testing.T) {
	for _, failEphemeral := range []bool{false, true} {
		name := "accepted"
		if failEphemeral {
			name = "restricted delivery rejected"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			public := apiResponse(`{"ok":true,"result":{"message_id":9,"chat":{"id":-100,"type":"supergroup"}}}`)
			ephemeral := apiResponse(`{"ok":true,"result":{"message_id":0,"ephemeral_message_id":77,"chat":{"id":-100,"type":"supergroup"}}}`)
			if failEphemeral {
				ephemeral = apiResponse(`{"ok":false,"error_code":403,"description":"PRIVATE response body"}`)
			}
			fake := testsupport.NewFakeTelegram(apiResponse(`{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"Test","username":"test_bot"}}`), public, apiResponse(`{"ok":true,"result":true}`), ephemeral, ephemeral, public)
			defer fake.Close()
			client, err := telegram.NewClient(ctx, telegram.ClientConfig{Token: "123:LOCAL_SECRET", BaseURL: fake.URL(), HTTPClient: fake.Client(), RequestTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			db, err := persistence.Open(ctx, persistence.Options{StorageDir: t.TempDir(), StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			migrations, err := persistence.RuntimeMigrations()
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Migrate(ctx, migrations); err != nil {
				t.Fatal(err)
			}
			now := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
			registry := interaction.NewRegistry()
			timeReads := 0
			// Handler time is read during dispatch; this also proves the processor
			// acknowledged the callback before entering registered behavior.
			if err = Register(registry, func() time.Time {
				timeReads++
				requests := fake.Requests()
				if timeReads == 1 && (len(requests) != 3 || !strings.HasSuffix(requests[2].Path, "/answerCallbackQuery")) {
					t.Error("callback not acknowledged before handler")
				}
				return now()
			}, englishPresentation(t)); err != nil {
				t.Fatal(err)
			}
			registry.Seal()
			var logs bytes.Buffer
			sink := observability.NewJSONSink(&logs)
			router := interaction.NewRouter(registry, db, db, nil, nil, interaction.RouterOptions{Now: now, Diagnostics: sink})
			processor, err := telegram.NewProcessor(router, telegram.NewDelivery(client, now), telegram.ProcessorOptions{Diagnostics: sink})
			if err != nil {
				t.Fatal(err)
			}
			updates := []string{
				`{"update_id":1,"message":{"message_id":1,"from":{"id":8},"chat":{"id":-100,"type":"supergroup"},"text":"/dev_smoke PRIVATE command args","entities":[{"type":"bot_command","offset":0,"length":10}]}}`,
				`{"update_id":2,"callback_query":{"id":"query","from":{"id":8},"message":{"message_id":9,"chat":{"id":-100,"type":"supergroup"}},"data":"dev_smoke:reply"}}`,
				`{"update_id":3,"message":{"message_id":0,"ephemeral_message_id":77,"from":{"id":8},"chat":{"id":-100,"type":"supergroup"},"text":"/dev_smoke_ephemeral","entities":[{"type":"bot_command","offset":0,"length":20}]}}`,
				`{"update_id":4,"message":{"message_id":4,"from":{"id":8},"chat":{"id":-100,"type":"supergroup"},"text":"PRIVATE ordinary body"}}`,
			}
			for _, raw := range updates {
				update, err := telegram.DecodeUpdate(json.RawMessage(raw))
				if err != nil {
					t.Fatal(err)
				}
				if err = processor.ProcessUpdate(ctx, update); err != nil {
					t.Fatal(err)
				}
			}
			requests := fake.Requests()
			if len(requests) != 6 {
				t.Fatalf("unexpected request count (possible fallback): %d", len(requests))
			}
			for i, method := range []string{"getMe", "sendMessage", "answerCallbackQuery", "sendMessage", "sendMessage", "sendMessage"} {
				if !strings.HasSuffix(requests[i].Path, "/"+method) {
					t.Fatalf("unexpected method at %d", i)
				}
			}
			ack := fields(t, requests[2])
			if ack["callback_query_id"] != "query" || ack["text"] != "" {
				t.Fatal("acknowledgement contains visible text or wrong identity")
			}
			for _, i := range []int{1, 3, 4, 5} {
				values := fields(t, requests[i])
				if values["chat_id"] != "-100" {
					t.Fatal("send left source group")
				}
				if strings.Contains(values["text"], "PRIVATE") {
					t.Fatal("incoming content echoed")
				}
				if i == 3 || i == 4 {
					var params map[string]any
					if err = json.Unmarshal([]byte(values["ephemeral_message_parameters"]), &params); err != nil {
						t.Fatal(err)
					}
					if params["receiver_user_id"] != float64(8) {
						t.Fatal("restricted target lost")
					}
					if i == 3 && params["callback_query_id"] != "query" {
						t.Fatal("callback authorization lost")
					}
					if i == 4 {
						var reply map[string]any
						if err = json.Unmarshal([]byte(values["reply_parameters"]), &reply); err != nil {
							t.Fatal(err)
						}
						if reply["ephemeral_message_id"] != float64(77) || reply["message_id"] != nil && reply["message_id"] != float64(0) {
							t.Fatal("ephemeral reply used ordinary ID")
						}
					}
				} else if values["ephemeral_message_parameters"] != "" {
					t.Fatal("public response changed visibility")
				}
			}
			if fields(t, requests[1])["reply_markup"] == "" {
				t.Fatal("public probe has no callback button")
			}
			cursor, err := db.CompletedUpdate(ctx)
			if err != nil || cursor != 4 {
				t.Fatal("updates not durably completed")
			}
			if strings.Contains(logs.String(), "PRIVATE") || strings.Contains(logs.String(), "Development smoke:") || strings.Contains(logs.String(), "LOCAL_SECRET") {
				t.Fatal("content leaked into diagnostics")
			}
			if failEphemeral && strings.Count(logs.String(), `"outcome":"failed"`) != 2 {
				t.Fatal("restricted failures not reported")
			}
		})
	}
}

func fields(t *testing.T, request testsupport.TelegramRequest) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"])
	values := map[string]string{}
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		value, err := io.ReadAll(part)
		if err != nil {
			t.Fatal(err)
		}
		values[part.FormName()] = string(value)
	}
	return values
}
