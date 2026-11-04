package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestDecodeUpdateFixtures(t *testing.T) {
	identity := interaction.Identity{Update: 42, Chat: -100, User: 7, ChatKind: runtimecontract.ChatKindSupergroup}
	tests := []struct {
		name, raw string
		status    DecodeStatus
		event     interaction.Event
		query     interaction.CallbackQueryID
	}{
		{"command", `{"update_id":42,"message":{"message_id":8,"chat":{"id":-100,"type":"supergroup"},"from":{"id":7},"text":"/help@sample_bot arg one","entities":[{"type":"bot_command","offset":0,"length":16}]}}`, DecodeMapped, interaction.Command{Identity: identity, MessageID: 8, Name: "help", Arguments: "arg one"}, ""},
		{"ordinary", `{"update_id":42,"message":{"message_id":8,"chat":{"id":-100,"type":"supergroup"},"from":{"id":7},"text":"hello 🌍"}}`, DecodeMapped, interaction.GroupMessage{Identity: identity, MessageID: 8, Text: "hello 🌍"}, ""},
		{"ordinary with username", `{"update_id":42,"message":{"message_id":8,"chat":{"id":-100,"type":"supergroup"},"from":{"id":7,"username":"unrelated_profile"},"text":"hello 🌍"}}`, DecodeMapped, interaction.GroupMessage{Identity: identity, MessageID: 8, Text: "hello 🌍"}, ""},
		{"media", `{"update_id":42,"message":{"message_id":8,"chat":{"id":-100,"type":"supergroup"},"from":{"id":7},"photo":[]}}`, DecodeMapped, interaction.GroupMessage{Identity: identity, MessageID: 8}, ""},
		{"callback", `{"update_id":42,"callback_query":{"id":"query","from":{"id":7},"message":{"message_id":8,"chat":{"id":-100,"type":"supergroup"}},"data":"opaque:data"}}`, DecodeMapped, interaction.Callback{Identity: identity, MessageID: 8, QueryID: "query", Data: "opaque:data"}, "query"},
		{"ephemeral", `{"update_id":42,"message":{"message_id":0,"ephemeral_message_id":9,"chat":{"id":-100,"type":"supergroup"},"from":{"id":7},"text":"hello"}}`, DecodeMapped, interaction.GroupMessage{Identity: identity, EphemeralMessageID: 9, Text: "hello"}, ""},
		{"unsupported", `{"update_id":42,"edited_message":{"text":"ignored"}}`, DecodeIgnored, nil, ""},
		{"private", `{"update_id":42,"message":{"message_id":8,"chat":{"id":7,"type":"private"},"from":{"id":7}}}`, DecodeIgnored, nil, ""},
		{"inline callback", `{"update_id":42,"callback_query":{"id":"query","from":{"id":7},"inline_message_id":"inline","data":"data"}}`, DecodeIgnored, nil, "query"},
		{"malformed body", `{"update_id":42,"message":"secret"}`, DecodeFailed, nil, ""},
		{"malformed nested", `{"update_id":42,"message":{"chat":{"id":"secret"}}}`, DecodeFailed, nil, ""},
		{"missing actor", `{"update_id":42,"message":{"message_id":8,"chat":{"id":-100,"type":"supergroup"}}}`, DecodeFailed, nil, ""},
		{"missing message identity", `{"update_id":42,"message":{"chat":{"id":-100,"type":"supergroup"},"from":{"id":7}}}`, DecodeFailed, nil, ""},
		{"missing callback identity", `{"update_id":42,"callback_query":{"from":{"id":7},"message":{"message_id":8,"chat":{"id":-100,"type":"supergroup"}},"data":"data"}}`, DecodeFailed, nil, ""},
		{"callback without data", `{"update_id":42,"callback_query":{"id":"query","from":{"id":7},"message":{"message_id":8,"chat":{"id":-100,"type":"supergroup"}},"game_short_name":"game"}}`, DecodeIgnored, nil, "query"},
		{"null message", `{"update_id":42,"message":null}`, DecodeFailed, nil, ""},
		{"ambiguous", `{"update_id":42,"message":{},"callback_query":{}}`, DecodeFailed, nil, ""},
		{"missing chat", `{"update_id":42,"message":{"message_id":8,"from":{"id":7},"text":"body"}}`, DecodeFailed, nil, ""},
		{"service", `{"update_id":42,"message":{"message_id":8,"from":{"id":7},"chat":{"id":-100,"type":"supergroup"},"new_chat_title":"new title"}}`, DecodeIgnored, nil, ""},
		{"inaccessible callback", `{"update_id":42,"callback_query":{"id":"query","from":{"id":7},"message":{"message_id":8,"date":0,"chat":{"id":-100,"type":"supergroup"}},"data":"opaque"}}`, DecodeIgnored, nil, "query"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeUpdate(json.RawMessage(tt.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got.UpdateID != 42 || got.Status != tt.status || !reflect.DeepEqual(got.Event, tt.event) || got.CallbackQueryID != tt.query {
				t.Fatalf("got %#v event %#v; want %s %#v query %q", got, got.Event, tt.status, tt.event, tt.query)
			}
		})
	}
}

func TestDecodeUpdateCommandEntities(t *testing.T) {
	for _, tt := range []struct {
		name           string
		text           string
		offset, length int
		status         DecodeStatus
		command        bool
	}{
		{"canonical", "/HELP 😀", 0, 5, DecodeMapped, true},
		{"nonleading", "😀 /help", 3, 5, DecodeMapped, false},
		{"oversized", "/help", 0, 50, DecodeFailed, false},
		{"invalid command", "/h-elp", 0, 6, DecodeFailed, false},
		{"empty target", "/help@", 0, 6, DecodeFailed, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			message := map[string]any{"message_id": 8, "chat": map[string]any{"id": -100, "type": "supergroup"}, "from": map[string]any{"id": 7}, "text": tt.text, "entities": []any{map[string]any{"type": "bot_command", "offset": tt.offset, "length": tt.length}}}
			raw, err := json.Marshal(map[string]any{"update_id": 42, "message": message})
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeUpdate(raw)
			if err != nil || got.Status != tt.status {
				t.Fatalf("got %#v %v", got, err)
			}
			command, ok := got.Event.(interaction.Command)
			if ok != tt.command {
				t.Fatalf("event %#v", got.Event)
			}
			if ok && (command.Name != "help" || command.Arguments != "😀") {
				t.Fatalf("command %#v", command)
			}
		})
	}
}

func TestDecodeUpdateUnsafeIdentity(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `[]`, `{"update_id":null}`, `{"update_id":0}`, `{"update_id":-1}`, `{"update_id":"secret"}`, `{"update_id":1.5}`, `{"update_id":9223372036854775808}`, `{"update_id":42,`} {
		got, err := DecodeUpdate(json.RawMessage(raw))
		if !errors.Is(err, ErrUpdateID) || got.UpdateID != 0 || got.Event != nil {
			t.Fatalf("unsafe identity: %#v %v", got, err)
		}
	}
}

func TestDecodeUpdateActorUsername(t *testing.T) {
	for _, tc := range []struct {
		name, from, want string
	}{
		{"available", `{"id":7,"username":"requesting_player"}`, "requesting_player"},
		{"absent", `{"id":7}`, ""},
		{"empty", `{"id":7,"username":""}`, ""},
		{"null", `{"id":7,"username":null}`, ""},
		{"presentation input", `{"id":7,"username":"player\nname"}`, "player\nname"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, kind := range []string{"command", "callback"} {
				t.Run(kind, func(t *testing.T) {
					var raw string
					if kind == "command" {
						raw = fmt.Sprintf(`{"update_id":42,"message":{"ephemeral_message_id":8,"from":%s,"chat":{"id":-100,"type":"supergroup"},"text":"/inventory","entities":[{"type":"bot_command","offset":0,"length":10}]}}`, tc.from)
					} else {
						raw = fmt.Sprintf(`{"update_id":42,"callback_query":{"id":"query","from":%s,"message":{"ephemeral_message_id":8,"from":{"id":99,"username":"message_author"},"chat":{"id":-100,"type":"supergroup"}},"data":"inv:opaque"}}`, tc.from)
					}
					got, err := DecodeUpdate(json.RawMessage(raw))
					if err != nil || got.Status != DecodeMapped {
						t.Fatalf("decode status %q, error %v", got.Status, err)
					}
					var identity interaction.Identity
					switch event := got.Event.(type) {
					case interaction.Command:
						identity = event.Identity
					case interaction.Callback:
						identity = event.Identity
					default:
						t.Fatalf("unexpected event type %T", got.Event)
					}
					want := interaction.Identity{Update: 42, Chat: -100, User: 7, ChatKind: runtimecontract.ChatKindSupergroup, ActorUsername: tc.want}
					if identity != want {
						t.Fatal("actor username or numeric identity changed")
					}
				})
			}
		})
	}
}

func TestCommandSourceAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name, extra string
		bot, chat   bool
		unix        int64
	}{
		{"human", `,"date":123`, false, false, 123},
		{"missing time", "", false, false, 0},
		{"bot", `,"date":123`, true, false, 123},
		{"anonymous", `,"date":123,"sender_chat":{"id":-8}`, false, true, 123},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(fmt.Sprintf(`{"update_id":1,"message":{"ephemeral_message_id":7,"from":{"id":42,"is_bot":%t},"chat":{"id":-9,"type":"supergroup"},"text":"/character","entities":[{"type":"bot_command","offset":0,"length":10}]%s}}`, tc.bot, tc.extra))
			got, err := DecodeUpdate(raw)
			if err != nil {
				t.Fatal(err)
			}
			command, ok := got.Event.(interaction.Command)
			if !ok {
				t.Fatalf("event %T", got.Event)
			}
			if command.ActorIsBot != tc.bot || command.SenderIsChat != tc.chat {
				t.Fatalf("metadata %+v", command)
			}
			if tc.unix == 0 {
				if !command.SentAt.IsZero() {
					t.Fatal("invented timestamp")
				}
			} else if command.SentAt.Unix() != tc.unix {
				t.Fatal("lost source timestamp")
			}
		})
	}
}

func TestCallbackPreservesBotActorIdentity(t *testing.T) {
	update, err := DecodeUpdate(json.RawMessage(`{"update_id":42,"callback_query":{"id":"query","from":{"id":7,"is_bot":true},"message":{"message_id":8,"chat":{"id":-100,"type":"supergroup"}},"data":"set:opaque"}}`))
	if err != nil {
		t.Fatal(err)
	}
	callback, ok := update.Event.(interaction.Callback)
	if !ok || !callback.ActorIsBot {
		t.Fatalf("bot actor identity lost: %+v", update)
	}
}
