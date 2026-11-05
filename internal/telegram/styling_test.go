package telegram

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func TestStyledContentSurvivesEveryDeliveryPath(t *testing.T) {
	for _, mode := range []string{"public", "ephemeral", "edit"} {
		t.Run(mode, func(t *testing.T) {
			result := `{"ok":true,"result":{"message_id":9,"chat":{"id":-100}}}`
			switch mode {
			case "ephemeral":
				result = `{"ok":true,"result":{"ephemeral_message_id":19,"chat":{"id":-100}}}`
			case "edit":
				result = `{"ok":true,"result":true}`
			}

			fake := testsupport.NewFakeTelegram(response(identity), response(result))
			defer fake.Close()

			delivery := NewDelivery(newTestClient(t, fake), func() time.Time { return ephemeralNow })
			styles := []runtimecontract.ButtonStyle{"", runtimecontract.ButtonPrimary, runtimecontract.ButtonSuccess, runtimecontract.ButtonDanger}
			content := runtimecontract.MessageContent{Text: "😀 title", Entities: []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 3, Length: 5}}, Buttons: [][]runtimecontract.InlineButton{{}}}
			for _, style := range styles {
				content.Buttons[0] = append(content.Buttons[0], runtimecontract.InlineButton{Text: "Action", CallbackData: "unchanged", Style: style})
			}

			var err error
			if mode == "public" {
				req := publicRequest()
				req.Content = content
				_, err = delivery.SendPublic(context.Background(), req)
			} else {
				req := ephemeralRequest()
				req.Content = content
				if mode == "edit" {
					req.EditMessageID = 19
					auth := req.Authorization.(runtimecontract.CallbackAuthorization)
					auth.OriginatedFromEphemeral = true
					req.Authorization = auth
				}

				_, err = delivery.SendEphemeral(context.Background(), req)
			}
			if err != nil {
				t.Fatal(err)
			}

			fields := requestFields(t, fake.Requests()[1])
			if fields["text"] != content.Text || fields["entities"] != `[{"type":"bold","offset":3,"length":5}]` || fields["parse_mode"] != "" {
				t.Fatalf("text or entities changed: %v", fields)
			}

			var markup struct {
				Rows [][]map[string]string `json:"inline_keyboard"`
			}
			if err := json.Unmarshal([]byte(fields["reply_markup"]), &markup); err != nil {
				t.Fatal(err)
			}

			want := []map[string]string{{"text": "Action", "callback_data": "unchanged"}, {"text": "Action", "callback_data": "unchanged", "style": "primary"}, {"text": "Action", "callback_data": "unchanged", "style": "success"}, {"text": "Action", "callback_data": "unchanged", "style": "danger"}}
			if len(markup.Rows) != 1 || !reflect.DeepEqual(markup.Rows[0], want) {
				t.Fatalf("button styling or default omission lost: %+v", markup)
			}
		})
	}
}
