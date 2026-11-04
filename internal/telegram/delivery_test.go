package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func publicRequest() runtimecontract.PublicResponse {
	return runtimecontract.PublicResponse{Destination: runtimecontract.GroupDestination{ChatID: -100, Kind: runtimecontract.ChatKindSupergroup}, Content: runtimecontract.MessageContent{Text: "Hello 😀"}}
}

func requestFields(t *testing.T, request testsupport.TelegramRequest) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	reader := multipart.NewReader(bytes.NewReader(request.Body), params["boundary"])
	fields := map[string]string{}
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
		if _, ok := fields[part.FormName()]; ok {
			t.Fatal("duplicate field")
		}
		fields[part.FormName()] = string(value)
	}
	return fields
}

func TestDeliveryPublicExactPayload(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":{"message_id":9,"chat":{"id":-100,"type":"supergroup"}}}`))
	defer fake.Close()
	delivery := NewDelivery(newTestClient(t, fake), nil)
	req := publicRequest()
	req.Content.Entities = []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 0, Length: 5}, {Kind: runtimecontract.TextEntityItalic, Offset: 0, Length: 5}, {Kind: runtimecontract.TextEntityCode, Offset: 6, Length: 2}, {Kind: runtimecontract.TextEntityTextLink, Offset: 0, Length: 5, URL: "https://example.org"}}
	req.Content.Buttons = [][]runtimecontract.InlineButton{{{Text: "One", CallbackData: "one"}, {Text: "Two", CallbackData: "two"}}, {{Text: "Three", CallbackData: "three"}}}
	receipt, err := delivery.SendPublic(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if receipt != (runtimecontract.DeliveryReceipt{ChatID: -100, MessageID: 9}) {
		t.Fatalf("receipt=%+v", receipt)
	}
	requests := fake.Requests()
	if len(requests) != 2 || requests[1].Path != "/bot42:secret/sendMessage" {
		t.Fatalf("requests=%v", requests)
	}
	fields := requestFields(t, requests[1])
	expected := map[string]string{"chat_id": "-100", "text": "Hello 😀", "entities": `[{"type":"bold","offset":0,"length":5},{"type":"italic","offset":0,"length":5},{"type":"code","offset":6,"length":2},{"type":"text_link","offset":0,"length":5,"url":"https://example.org"}]`, "reply_markup": `{"inline_keyboard":[[{"text":"One","callback_data":"one"},{"text":"Two","callback_data":"two"}],[{"text":"Three","callback_data":"three"}]]}`}
	if !reflect.DeepEqual(fields, expected) {
		t.Fatalf("fields=%v, want %v", fields, expected)
	}
}

func TestDeliveryCallbackInvisible(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`))
	defer fake.Close()
	err := NewDelivery(newTestClient(t, fake), nil).AcknowledgeCallback(context.Background(), runtimecontract.CallbackAcknowledgement{CallbackQueryID: "callback"})
	if err != nil {
		t.Fatal(err)
	}
	requests := fake.Requests()
	if len(requests) != 2 || requests[1].Path != "/bot42:secret/answerCallbackQuery" {
		t.Fatalf("requests=%v", requests)
	}
	if fields := requestFields(t, requests[1]); !reflect.DeepEqual(fields, map[string]string{"callback_query_id": "callback"}) {
		t.Fatalf("visible callback fields=%v", fields)
	}
}

func TestDeliveryInvalidBeforeIO(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*runtimecontract.PublicResponse)
		field  string
	}{
		{"missing chat", func(r *runtimecontract.PublicResponse) { r.Destination.ChatID = 0 }, "destination.chat_id"},
		{"private chat", func(r *runtimecontract.PublicResponse) { r.Destination.ChatID = 8 }, "destination.chat_id"},
		{"unsupported chat", func(r *runtimecontract.PublicResponse) { r.Destination.Kind = "private" }, "destination.kind"},
		{"empty text", func(r *runtimecontract.PublicResponse) { r.Content.Text = "" }, "content.text"},
		{"long text", func(r *runtimecontract.PublicResponse) { r.Content.Text = strings.Repeat("x", 4097) }, "content.text"},
		{"invalid entity", func(r *runtimecontract.PublicResponse) {
			r.Content.Entities = []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 7, Length: 1}}
		}, "content.entities.range"},
		{"invalid callback", func(r *runtimecontract.PublicResponse) {
			r.Content.Buttons = [][]runtimecontract.InlineButton{{{Text: "button", CallbackData: strings.Repeat("x", 65)}}}
		}, "content.buttons.callback_data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := testsupport.NewFakeTelegram(response(identity))
			defer fake.Close()
			d := NewDelivery(newTestClient(t, fake), nil)
			req := publicRequest()
			tc.change(&req)
			receipt, err := d.SendPublic(context.Background(), req)
			var validation *runtimecontract.ValidationError
			if !errors.As(err, &validation) || validation.Field != tc.field {
				t.Fatalf("err=%v", err)
			}
			if receipt != (runtimecontract.DeliveryReceipt{}) || len(fake.Requests()) != 1 {
				t.Fatal("invalid request delivered")
			}
			if outcome := Outcome(receipt, err); outcome.Kind != runtimecontract.DeliveryPermanentFailure || outcome.ReasonCode != "validation" || outcome.Receipt != nil {
				t.Fatalf("invalid request outcome=%+v", outcome)
			}
		})
	}
	fake := testsupport.NewFakeTelegram(response(identity))
	defer fake.Close()
	err := NewDelivery(newTestClient(t, fake), nil).AcknowledgeCallback(context.Background(), runtimecontract.CallbackAcknowledgement{})
	var validation *runtimecontract.ValidationError
	if !errors.As(err, &validation) || validation.Field != "callback_query_id" || len(fake.Requests()) != 1 {
		t.Fatalf("err=%v requests=%v", err, fake.Requests())
	}
}

func TestDeliveryUnconfirmedAcceptance(t *testing.T) {
	for _, body := range []string{`{"ok":true}`, `{"ok":true,"result":null}`, `{"ok":true,"result":{}}`, `{"ok":true,"result":{"message_id":0,"chat":{"id":-100}}}`, `{"ok":true,"result":{"message_id":9,"chat":{"id":-200}}}`, `{"ok":true,"result":{"message_id":9,"ephemeral_message_id":10,"chat":{"id":-100}}}`, `{"ok":true,"result":"invalid"}`} {
		fake := testsupport.NewFakeTelegram(response(identity), response(body))
		d := NewDelivery(newTestClient(t, fake), nil)
		receipt, err := d.SendPublic(context.Background(), publicRequest())
		_ = assertKind(t, err, ErrorIndeterminate)
		if receipt != (runtimecontract.DeliveryReceipt{}) || len(fake.Requests()) != 2 {
			t.Fatal("unconfirmed send accepted or retried")
		}
		fake.Close()
	}
	for _, result := range []string{"false", "null", `"invalid"`} {
		fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":`+result+`}`))
		d := NewDelivery(newTestClient(t, fake), nil)
		_ = assertKind(t, d.AcknowledgeCallback(context.Background(), runtimecontract.CallbackAcknowledgement{CallbackQueryID: "id"}), ErrorIndeterminate)
		if len(fake.Requests()) != 2 || fake.Requests()[1].Path != "/bot42:secret/answerCallbackQuery" {
			t.Fatal("unconfirmed acknowledgement retried or sent a chat message")
		}
		fake.Close()
	}
}

func TestDeliveryErrorsAndOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response testsupport.TelegramResponse
		kind     ErrorKind
		outcome  runtimecontract.DeliveryOutcomeKind
		delay    time.Duration
	}{
		{"rate", response(`{"ok":false,"error_code":429,"description":"secret","parameters":{"retry_after":17}}`), ErrorRateLimit, runtimecontract.DeliveryRetryableFailure, 17 * time.Second},
		{"forbidden", response(`{"ok":false,"error_code":403,"description":"secret"}`), ErrorAuthorization, runtimecontract.DeliveryPermanentFailure, 0},
		{"invalid", response(`{"ok":false,"error_code":400,"description":"secret"}`), ErrorValidation, runtimecontract.DeliveryPermanentFailure, 0},
		{"conflict", response(`{"ok":false,"error_code":409,"description":"secret"}`), ErrorConflict, runtimecontract.DeliveryPermanentFailure, 0},
		{"permanent", response(`{"ok":false,"error_code":404,"description":"secret"}`), ErrorPermanent, runtimecontract.DeliveryPermanentFailure, 0},
		{"transient", response(`{"ok":false,"error_code":503,"description":"secret"}`), ErrorTransient, runtimecontract.DeliveryRetryableFailure, 0},
		{"dropped", testsupport.TelegramResponse{DropConnection: true}, ErrorIndeterminate, runtimecontract.DeliveryIndeterminate, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := testsupport.NewFakeTelegram(response(identity), tc.response)
			defer fake.Close()
			d := NewDelivery(newTestClient(t, fake), nil)
			receipt, err := d.SendPublic(context.Background(), publicRequest())
			_ = assertKind(t, err, tc.kind)
			outcome := Outcome(receipt, err)
			if outcome.Kind != tc.outcome || outcome.RetryAfter != tc.delay || outcome.Receipt != nil || outcome.ReasonCode != string(tc.kind) {
				t.Fatalf("outcome=%+v", outcome)
			}
			if len(fake.Requests()) != 2 {
				t.Fatal("unexpected retry")
			}
			encoded, _ := json.Marshal(outcome)
			if strings.Contains(string(encoded), "secret") {
				t.Fatal("unsafe outcome")
			}
		})
	}
}

func TestDeliveryCancellationAndPlainPayload(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":{"message_id":8,"chat":{"id":-100}}}`))
	defer fake.Close()
	d := NewDelivery(newTestClient(t, fake), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	receipt, err := d.SendPublic(ctx, publicRequest())
	_ = assertKind(t, err, ErrorCancellation)
	if !errors.Is(err, context.Canceled) || len(fake.Requests()) != 1 || Outcome(receipt, err).Kind != runtimecontract.DeliveryCancelled {
		t.Fatal("cancellation lost")
	}
	receipt, err = d.SendPublic(context.Background(), publicRequest())
	if err != nil {
		t.Fatal(err)
	}
	outcome := Outcome(receipt, nil)
	if outcome.Kind != runtimecontract.DeliveryAccepted || outcome.Receipt == nil || *outcome.Receipt != receipt {
		t.Fatalf("outcome=%+v", outcome)
	}
	if fields := requestFields(t, fake.Requests()[1]); !reflect.DeepEqual(fields, map[string]string{"chat_id": "-100", "text": "Hello 😀"}) {
		t.Fatalf("fields=%v", fields)
	}
	if got := Outcome(runtimecontract.DeliveryReceipt{}, errors.New("secret")); got.Kind != runtimecontract.DeliveryIndeterminate || got.ReasonCode != "indeterminate" {
		t.Fatalf("unknown error outcome=%+v", got)
	}
}
