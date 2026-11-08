package bank

import (
	"encoding/json"
	"strings"
	"testing"

	app "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/modules/items"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

func order(direction domain.Direction) domain.Order {
	return domain.Order{
		ID: "order",
		Quote: domain.Quote{
			ID:        "quote",
			Group:     -1,
			Player:    42,
			Character: "character",
			Entry:     "entry",
			Direction: direction,
			Snapshot: items.Snapshot{
				Version:   1,
				CatalogID: "bread",
				Mode:      items.Stackable,
				Rarity:    "common",
				English: items.Text{
					Name:        "Bread",
					Description: "Food",
				},
				Russian: items.Text{
					Name:        "Хлеб",
					Description: "Еда",
				},
			},
			Quantity:     1,
			Unit:         "9007199254740993",
			Total:        "9007199254740993",
			BankID:       99,
			TreasuryID:   2,
			DeliveryChat: -1,
		},
	}
}

func TestExactFinancialEnvelopes(t *testing.T) {
	for _, direction := range []domain.Direction{domain.Buy, domain.Sell} {
		r, err := (Protocol{}).Financial(order(direction), "transport", "financial")
		if err != nil {
			t.Fatal(err)
		}
		body, err := (Protocol{}).Encode(r)
		if err != nil {
			t.Fatal(err)
		}
		var envelope map[string]json.RawMessage
		if err = json.Unmarshal([]byte(body), &envelope); err != nil || len(envelope) != 4 {
			t.Fatalf("envelope: %s %v", body, err)
		}
		want := `{"accountId":2,"payer":{"type":"telegram_id","telegramUserId":42},"amount":"9007199254740993","idempotencyKey":"financial","comment":"RepliPG order order","chatId":-1}`
		if direction == domain.Sell {
			want = `{"accountId":2,"recipient":{"type":"telegram_id","telegramUserId":42},"amount":"9007199254740993","idempotencyKey":"financial"}`
		}
		if string(envelope["params"]) != want {
			t.Fatalf("params: %s", envelope["params"])
		}
	}
	status, err := (Protocol{}).Status(order(domain.Buy), "fresh-read", "payment")
	if err != nil || status.Method != PaymentStatus || string(status.Params) != `{"requestId":"payment"}` {
		t.Fatalf("status: %+v %v", status, err)
	}
}

func TestFinancialStatesAndOptionalFields(t *testing.T) {
	o := order(domain.Buy)
	r, err := (Protocol{}).Financial(o, "transport", "financial")
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{
		`{"requestId":"payment","state":"pending"}`,
		`{"requestId":"payment","state":"rejected"}`,
		`{"requestId":"payment","state":"failed","reason":"insufficient_funds"}`,
		`{"requestId":"payment","state":"paid","transaction":{"transactionId":"9007199254740993","senderAccountId":456,"recipient":{"accountId":2},"amount":"9007199254740993","createdAt":"2026-01-02T03:04:05.000Z"}}`,
	} {
		body := `{"jsonrpc":"2.0","id":"transport","result":{"data":` + data + `,"operation":{"actionId":"` + Invoice + `","operationId":"non-uuid"}}}`
		result, err := (Protocol{}).Decode(o, r, body)
		if err != nil || result.PaymentID != "payment" || result.State == "" {
			t.Fatalf("financial state: %+v %v", result, err)
		}
	}
	o = order(domain.Sell)
	r, err = (Protocol{}).Financial(o, "transport", "financial")
	if err != nil {
		t.Fatal(err)
	}
	for _, optional := range []string{"", `,"telegramUserId":42,"username":"example"`} {
		body := `{"jsonrpc":"2.0","id":"transport","result":{"data":{"transactionId":"501","senderAccountId":2,"recipient":{"accountId":456` + optional + `},"amount":"9007199254740993","createdAt":"date"}}}`
		if evidence, err := (Protocol{}).Decode(o, r, body); err != nil || evidence.TransactionID != "501" {
			t.Fatalf("optional recipient: %+v %v", evidence, err)
		}
	}
}

func TestErrorsNeverInventFinancialReason(t *testing.T) {
	o := order(domain.Sell)
	r, _ := (Protocol{}).Financial(o, "transport", "financial")
	for _, fixture := range []struct {
		code    string
		message string
	}{
		{
			code:    "-32601",
			message: "Method unavailable",
		},
		{
			code:    "-32602",
			message: "Invalid params",
		},
		{
			code:    "-32603",
			message: "Internal error",
		},
		{
			code:    "-32001",
			message: "Request conflict",
		},
		{
			code:    "-32003",
			message: "Response too large",
		},
	} {
		body := `{"jsonrpc":"2.0","id":"transport","error":{"code":` + fixture.code + `,"message":"` + fixture.message + `"}}`
		evidence, err := (Protocol{}).Decode(o, r, body)
		if err != nil || evidence.State != "" || evidence.TransactionID != "" || evidence.Diagnostic != fixture.message {
			t.Fatalf("generic error: %+v %v", evidence, err)
		}
	}
}

func TestRejectMalformedAndMismatchedReplies(t *testing.T) {
	o := order(domain.Buy)
	r, _ := (Protocol{}).Financial(o, "transport", "financial")
	valid := `{"jsonrpc":"2.0","id":"transport","result":{"data":{"requestId":"payment","state":"paid","transaction":{"transactionId":"501","senderAccountId":456,"recipient":{"accountId":2},"amount":"9007199254740993","createdAt":"date"}}}}`
	for _, invalid := range []string{
		strings.Replace(valid, `"id":"transport"`, `"id":1`, 1),
		strings.Replace(valid, `"id":"transport"`, `"id":null`, 1),
		strings.Replace(valid, `"id":"transport"`, `"id":"foreign"`, 1),
		strings.Replace(valid, `"amount":"9007199254740993"`, `"amount":9007199254740993`, 1),
		strings.Replace(valid, `"amount":"9007199254740993"`, `"amount":"1"`, 1),
		strings.Replace(valid, `"accountId":2`, `"accountId":3`, 1),
		strings.Replace(valid, `"jsonrpc":"2.0"`, `"jsonrpc":"2.0","jsonrpc":"2.0"`, 1),
		strings.Replace(valid, `"jsonrpc":"2.0"`, `"jsonrpc":"2.0","method":"evil"`, 1),
		`{"jsonrpc":"2.0","id":"transport","result":{"data":{"requestId":"payment","state":"paid"}}}`,
		`{"jsonrpc":"2.0","id":"transport","result":{"data":{"requestId":"payment","state":"failed"}}}`,
		`{"jsonrpc":"2.0","id":"transport","result":null}`,
		`[` + valid + `]`,
		valid + valid,
	} {
		if _, err := (Protocol{}).Decode(o, r, invalid); err == nil {
			t.Errorf("accepted invalid reply %s", invalid)
		}
	}
	if _, err := (Protocol{}).Decode(o, r, valid); err != nil {
		t.Fatal(err)
	}
	r.PaymentID = "different"
	if _, err := (Protocol{}).Decode(o, r, valid); err == nil {
		t.Fatal("accepted different request ID")
	}
}

func TestEncodingLimits(t *testing.T) {
	for _, raw := range []string{
		`{"x":"` + strings.Repeat("a", 16385) + `"}`,
		`{"x":"` + strings.Repeat("😀", 2049) + `"}`,
		strings.Repeat(`{"x":`, 34) + `1` + strings.Repeat("}", 34),
		string([]byte{0xff}),
	} {
		if err := limits([]byte(raw), true); err == nil {
			t.Fatal("accepted envelope beyond limits")
		}
	}
	if _, err := (Protocol{}).Encode(app.Request{
		ID:     strings.Repeat("a", 129),
		Method: Invoice,
		Params: json.RawMessage(`{}`),
	}); err == nil {
		t.Fatal("accepted oversized ID")
	}
}
