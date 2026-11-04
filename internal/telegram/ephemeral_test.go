package telegram

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

var ephemeralNow = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func ephemeralRequest() runtimecontract.EphemeralResponse {
	return runtimecontract.EphemeralResponse{Destination: publicRequest().Destination, TargetUserID: 7, Authorization: runtimecontract.CallbackAuthorization{CallbackQueryID: "callback", ValidUntil: ephemeralNow.Add(15 * time.Second)}, Content: publicRequest().Content}
}

func TestEphemeralExactRestrictedPayloads(t *testing.T) {
	callback := runtimecontract.CallbackAuthorization{CallbackQueryID: "callback", ValidUntil: ephemeralNow.Add(15 * time.Second)}
	incoming := runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: 23, ValidUntil: ephemeralNow.Add(15 * time.Second)}
	admin := runtimecontract.AdministratorAuthorization{}
	for _, tc := range []struct {
		name              string
		auth              runtimecontract.EphemeralAuthorization
		replace           bool
		parameters, reply string
	}{
		{"callback", callback, false, `{"receiver_user_id":7,"callback_query_id":"callback"}`, ""},
		{"callback pointer replacement", &callback, true, `{"receiver_user_id":7,"callback_query_id":"callback","replace_callback_query_message":true}`, ""},
		{"incoming", incoming, false, `{"receiver_user_id":7}`, `{"ephemeral_message_id":23}`},
		{"incoming pointer", &incoming, false, `{"receiver_user_id":7}`, `{"ephemeral_message_id":23}`},
		{"administrator", admin, false, `{"receiver_user_id":7}`, ""},
		{"administrator pointer", &admin, false, `{"receiver_user_id":7}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":{"message_id":0,"ephemeral_message_id":19,"chat":{"id":-100,"type":"supergroup"}}}`))
			defer fake.Close()
			d := NewDelivery(newTestClient(t, fake), func() time.Time { return ephemeralNow })
			req := ephemeralRequest()
			req.Authorization = tc.auth
			req.ReplaceCallbackMessage = tc.replace
			req.Content.Entities = []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 0, Length: 5}}
			req.Content.Buttons = [][]runtimecontract.InlineButton{{{Text: "button", CallbackData: "data"}}}
			receipt, err := d.SendEphemeral(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if receipt != (runtimecontract.DeliveryReceipt{ChatID: -100, EphemeralMessageID: 19}) {
				t.Fatalf("receipt=%+v", receipt)
			}
			requests := fake.Requests()
			if len(requests) != 2 || requests[1].Path != "/bot42:secret/sendMessage" {
				t.Fatalf("requests=%v", requests)
			}
			want := map[string]string{"chat_id": "-100", "text": "Hello 😀", "ephemeral_message_parameters": tc.parameters, "entities": `[{"type":"bold","offset":0,"length":5}]`, "reply_markup": `{"inline_keyboard":[[{"text":"button","callback_data":"data"}]]}`}
			if tc.reply != "" {
				want["reply_parameters"] = tc.reply
			}
			if got := requestFields(t, requests[1]); !reflect.DeepEqual(got, want) {
				t.Fatalf("fields=%v want=%v", got, want)
			}
		})
	}
}

func TestEphemeralInvalidBeforeIO(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*runtimecontract.EphemeralResponse)
		field  string
	}{
		{"missing chat", func(r *runtimecontract.EphemeralResponse) { r.Destination.ChatID = 0 }, "destination.chat_id"},
		{"private chat", func(r *runtimecontract.EphemeralResponse) { r.Destination.ChatID = 7 }, "destination.chat_id"},
		{"unsupported chat", func(r *runtimecontract.EphemeralResponse) { r.Destination.Kind = "channel" }, "destination.kind"},
		{"missing user", func(r *runtimecontract.EphemeralResponse) { r.TargetUserID = 0 }, "target.user_id"},
		{"negative user", func(r *runtimecontract.EphemeralResponse) { r.TargetUserID = -7 }, "target.user_id"},
		{"bot user", func(r *runtimecontract.EphemeralResponse) { r.TargetIsBot = true }, "target.user_id"},
		{"missing authorization", func(r *runtimecontract.EphemeralResponse) { r.Authorization = nil }, "authorization"},
		{"nil callback", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = (*runtimecontract.CallbackAuthorization)(nil)
		}, "authorization"},
		{"nil incoming", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = (*runtimecontract.IncomingEphemeralAuthorization)(nil)
		}, "authorization"},
		{"nil admin", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = (*runtimecontract.AdministratorAuthorization)(nil)
		}, "authorization"},
		{"missing callback", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = runtimecontract.CallbackAuthorization{ValidUntil: ephemeralNow.Add(time.Second)}
		}, "authorization.callback_query_id"},
		{"missing expiry", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = runtimecontract.CallbackAuthorization{CallbackQueryID: "callback"}
		}, "authorization.valid_until"},
		{"expired callback", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = runtimecontract.CallbackAuthorization{CallbackQueryID: "callback", ValidUntil: ephemeralNow.Add(-time.Nanosecond)}
		}, "authorization.valid_until"},
		{"exact fifteen second boundary", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = runtimecontract.CallbackAuthorization{CallbackQueryID: "callback", ValidUntil: ephemeralNow}
		}, "authorization.valid_until"},
		{"expired incoming", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: 23, ValidUntil: ephemeralNow}
		}, "authorization.valid_until"},
		{"invalid incoming id", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = runtimecontract.IncomingEphemeralAuthorization{ValidUntil: ephemeralNow.Add(time.Second)}
		}, "authorization.ephemeral_message_id"},
		{"replace ephemeral callback", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = runtimecontract.CallbackAuthorization{CallbackQueryID: "callback", OriginatedFromEphemeral: true, ValidUntil: ephemeralNow.Add(time.Second)}
			r.ReplaceCallbackMessage = true
		}, "authorization.replace_callback_message"},
		{"replace incoming", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: 23, ValidUntil: ephemeralNow.Add(time.Second)}
			r.ReplaceCallbackMessage = true
		}, "authorization.replace_callback_message"},
		{"replace admin", func(r *runtimecontract.EphemeralResponse) {
			r.Authorization = runtimecontract.AdministratorAuthorization{}
			r.ReplaceCallbackMessage = true
		}, "authorization.replace_callback_message"},
		{"empty text", func(r *runtimecontract.EphemeralResponse) { r.Content.Text = "" }, "content.text"},
		{"long text", func(r *runtimecontract.EphemeralResponse) { r.Content.Text = strings.Repeat("x", 4097) }, "content.text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := testsupport.NewFakeTelegram(response(identity))
			defer fake.Close()
			d := NewDelivery(newTestClient(t, fake), func() time.Time { return ephemeralNow })
			req := ephemeralRequest()
			tc.change(&req)
			receipt, err := d.SendEphemeral(context.Background(), req)
			var invalid *runtimecontract.ValidationError
			if !errors.As(err, &invalid) || invalid.Field != tc.field {
				t.Fatalf("err=%v want field=%s", err, tc.field)
			}
			if receipt != (runtimecontract.DeliveryReceipt{}) || len(fake.Requests()) != 1 {
				t.Fatal("invalid request performed I/O")
			}
			if outcome := Outcome(receipt, err); outcome.Kind != runtimecontract.DeliveryPermanentFailure || outcome.ReasonCode != "validation" {
				t.Fatalf("outcome=%+v", outcome)
			}
		})
	}
}

func TestEphemeralFailureWithoutFallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		result  testsupport.TelegramResponse
		kind    ErrorKind
		outcome runtimecontract.DeliveryOutcomeKind
		delay   time.Duration
	}{
		{"forbidden", response(`{"ok":false,"error_code":403,"description":"private content secret"}`), ErrorAuthorization, runtimecontract.DeliveryPermanentFailure, 0},
		{"invalid", response(`{"ok":false,"error_code":400}`), ErrorValidation, runtimecontract.DeliveryPermanentFailure, 0},
		{"rate limit", response(`{"ok":false,"error_code":429,"parameters":{"retry_after":4}}`), ErrorRateLimit, runtimecontract.DeliveryRetryableFailure, 4 * time.Second},
		{"dropped", testsupport.TelegramResponse{DropConnection: true}, ErrorIndeterminate, runtimecontract.DeliveryIndeterminate, 0},
		{"missing result", response(`{"ok":true}`), ErrorIndeterminate, runtimecontract.DeliveryIndeterminate, 0},
		{"null result", response(`{"ok":true,"result":null}`), ErrorIndeterminate, runtimecontract.DeliveryIndeterminate, 0},
		{"empty result", response(`{"ok":true,"result":{}}`), ErrorIndeterminate, runtimecontract.DeliveryIndeterminate, 0},
		{"wrong chat", response(`{"ok":true,"result":{"message_id":0,"ephemeral_message_id":19,"chat":{"id":-200}}}`), ErrorIndeterminate, runtimecontract.DeliveryIndeterminate, 0},
		{"ordinary receipt", response(`{"ok":true,"result":{"message_id":8,"chat":{"id":-100}}}`), ErrorIndeterminate, runtimecontract.DeliveryIndeterminate, 0},
		{"conflicting receipt", response(`{"ok":true,"result":{"message_id":8,"ephemeral_message_id":19,"chat":{"id":-100}}}`), ErrorIndeterminate, runtimecontract.DeliveryIndeterminate, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := testsupport.NewFakeTelegram(response(identity), tc.result)
			defer fake.Close()
			d := NewDelivery(newTestClient(t, fake), func() time.Time { return ephemeralNow })
			receipt, err := d.SendEphemeral(context.Background(), ephemeralRequest())
			_ = assertKind(t, err, tc.kind)
			outcome := Outcome(receipt, err)
			if receipt != (runtimecontract.DeliveryReceipt{}) || outcome.Kind != tc.outcome || outcome.RetryAfter != tc.delay || outcome.ReasonCode != string(tc.kind) {
				t.Fatalf("receipt=%+v outcome=%+v", receipt, outcome)
			}
			requests := fake.Requests()
			if len(requests) != 2 || requests[1].Path != "/bot42:secret/sendMessage" {
				t.Fatalf("fallback or retry: %v", requests)
			}
			fields := requestFields(t, requests[1])
			if fields["chat_id"] != "-100" || fields["ephemeral_message_parameters"] == "" {
				t.Fatalf("visibility changed: %v", fields)
			}
		})
	}
}

func TestEphemeralAcceptanceAtZeroOrdinaryID(t *testing.T) {
	for _, body := range []string{
		`{"ok":true,"result":{"message_id":0,"ephemeral_message_id":19,"chat":{"id":-100}}}`,
		`{"ok":true,"result":{"message_id":0,"chat":{"id":-100}}}`,
	} {
		fake := testsupport.NewFakeTelegram(response(identity), response(body))
		d := NewDelivery(newTestClient(t, fake), func() time.Time { return ephemeralNow.Add(15*time.Second - time.Nanosecond) })
		receipt, err := d.SendEphemeral(context.Background(), ephemeralRequest())
		if err != nil || receipt.MessageID != 0 || receipt.ChatID != -100 || Outcome(receipt, err).Kind != runtimecontract.DeliveryAccepted {
			t.Fatalf("receipt=%+v err=%v", receipt, err)
		}
		fake.Close()
	}
}

func TestEphemeralCancelledBeforeIO(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity))
	defer fake.Close()
	d := NewDelivery(newTestClient(t, fake), func() time.Time { return ephemeralNow })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receipt, err := d.SendEphemeral(ctx, ephemeralRequest())
	_ = assertKind(t, err, ErrorCancellation)
	if !errors.Is(err, context.Canceled) || len(fake.Requests()) != 1 || Outcome(receipt, err).Kind != runtimecontract.DeliveryCancelled {
		t.Fatal("cancelled send performed I/O or lost cancellation")
	}
}

var _ runtimecontract.Sender = (*Delivery)(nil)
