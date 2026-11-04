package telegram

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestDecodeCallbackSource(t *testing.T) {
	for _, tc := range []struct {
		name, fields string
		want         *interaction.CallbackSource
	}{
		{"exact formatted source", `"text":"😀 Player","entities":[{"type":"bold","offset":0,"length":2},{"type":"italic","offset":3,"length":6},{"type":"text_link","offset":3,"length":6,"url":"tg://user?id=7"}],"receiver_user":{"id":7},"reply_markup":{"inline_keyboard":[[{"text":"Back","callback_data":"inventory:back"},{"text":"Share","callback_data":"share:v1:abc"}],[{"text":"External","url":"https://example.com"}]]}`, &interaction.CallbackSource{Body: runtimecontract.MessageContent{Text: "😀 Player", Entities: []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 0, Length: 2}, {Kind: runtimecontract.TextEntityItalic, Offset: 3, Length: 6}, {Kind: runtimecontract.TextEntityTextLink, Offset: 3, Length: 6, URL: "tg://user?id=7"}}}, CallbackData: []string{"inventory:back", "share:v1:abc"}, ReceiverUserID: 7, EphemeralMessageID: 9}},
		{"receiver mismatch remains evidence", `"text":"Player","receiver_user":{"id":8}`, &interaction.CallbackSource{Body: runtimecontract.MessageContent{Text: "Player"}, ReceiverUserID: 8, EphemeralMessageID: 9}},
		{"receiver absent", `"text":"Player"`, &interaction.CallbackSource{Body: runtimecontract.MessageContent{Text: "Player"}, EphemeralMessageID: 9}},
		{"receiver null", `"text":"Player","receiver_user":null`, &interaction.CallbackSource{Body: runtimecontract.MessageContent{Text: "Player"}, EphemeralMessageID: 9}},
		{"code", `"text":"Player","entities":[{"type":"code","offset":0,"length":6}]`, &interaction.CallbackSource{Body: runtimecontract.MessageContent{Text: "Player", Entities: []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityCode, Length: 6}}}, EphemeralMessageID: 9}},
		{"lexical", `"text":"@a #b /c https://x a@b.co $USD 123","entities":[{"type":"mention","offset":0,"length":2},{"type":"hashtag","offset":3,"length":2},{"type":"bot_command","offset":6,"length":2},{"type":"url","offset":9,"length":9},{"type":"email","offset":19,"length":6},{"type":"cashtag","offset":26,"length":4},{"type":"phone_number","offset":31,"length":3}]`, &interaction.CallbackSource{Body: runtimecontract.MessageContent{Text: "@a #b /c https://x a@b.co $USD 123"}, EphemeralMessageID: 9}},
		{"no body", `"reply_markup":{"inline_keyboard":[]}`, nil},
		{"unsupported formatting", `"text":"Player","entities":[{"type":"underline","offset":0,"length":6}]`, nil},
		{"unknown entity", `"text":"Player","entities":[{"type":"future_entity","offset":0,"length":6}]`, nil},
		{"surrogate start", `"text":"😀x","entities":[{"type":"bold","offset":1,"length":1}]`, nil},
		{"surrogate end lexical", `"text":"😀x","entities":[{"type":"mention","offset":0,"length":1}]`, nil},
		{"negative lexical", `"text":"Player","entities":[{"type":"url","offset":-1,"length":1}]`, nil},
		{"out of range lexical", `"text":"Player","entities":[{"type":"mention","offset":0,"length":7}]`, nil},
		{"overflow range", `"text":"Player","entities":[{"type":"bold","offset":9223372036854775807,"length":9223372036854775807}]`, nil},
		{"zero length lexical", `"text":"Player","entities":[{"type":"mention","offset":0,"length":0}]`, nil},
		{"missing offset", `"text":"Player","entities":[{"type":"bold","length":6}]`, nil},
		{"malformed entity", `"text":"Player","entities":[{"type":"bold","offset":"bad","length":6}]`, nil},
		{"malformed entities shape", `"text":"Player","entities":{}`, nil},
		{"invalid link", `"text":"Player","entities":[{"type":"text_link","offset":0,"length":6}]`, nil},
		{"ambiguous entity url", `"text":"Player","entities":[{"type":"bold","offset":0,"length":6,"url":"https://example.com"}]`, nil},
		{"malformed receiver", `"text":"Player","receiver_user":{"id":-1}`, nil},
		{"malformed keyboard", `"text":"Player","reply_markup":{"inline_keyboard":[[{"callback_data":7}]]}`, nil},
		{"invalid keyboard token", `"text":"Player","reply_markup":{"inline_keyboard":[[{"callback_data":""}]]}`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`{"update_id":42,"callback_query":{"id":"query","from":{"id":7},"message":{"ephemeral_message_id":9,"date":123,"chat":{"id":-100,"type":"supergroup"},` + tc.fields + `},"data":"opaque:data"}}`)
			got, err := DecodeUpdate(raw)
			if err != nil || got.Status != DecodeMapped {
				t.Fatalf("callback routing changed: status=%s err=%v", got.Status, err)
			}
			callback, ok := got.Event.(interaction.Callback)
			if !ok || callback.Data != "opaque:data" || callback.QueryID != "query" || callback.EphemeralMessageID != 9 {
				t.Fatal("callback identity changed")
			}
			if !reflect.DeepEqual(callback.Source, tc.want) {
				t.Fatal("source evidence differs from expected projection")
			}
		})
	}
}

func TestDecodeCallbackSourceInaccessibleAndOrdinary(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		status        DecodeStatus
	}{
		{"inaccessible ephemeral", `"ephemeral_message_id":9,"date":0`, DecodeIgnored},
		{"ordinary source", `"message_id":8,"date":123`, DecodeMapped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(`{"update_id":42,"callback_query":{"id":"query","from":{"id":7},"message":{` + tc.message + `,"chat":{"id":-100,"type":"supergroup"},"text":"Player","reply_markup":{"inline_keyboard":[[{"callback_data":"share:v1:abc"}]]}},"data":"share:v1:abc"}}`)
			got, err := DecodeUpdate(raw)
			if err != nil || got.Status != tc.status || got.CallbackQueryID != "query" {
				t.Fatalf("callback routing changed: status=%s err=%v", got.Status, err)
			}
			if got.Event != nil {
				callback, ok := got.Event.(interaction.Callback)
				if !ok || callback.Source != nil {
					t.Fatal("ordinary source must remain unavailable for sharing")
				}
			}
		})
	}
}
