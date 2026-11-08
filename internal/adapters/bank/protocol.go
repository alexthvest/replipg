// Package bank implements the external bank's JSON-RPC contract without I/O.
package bank

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf16"
	"unicode/utf8"

	app "github.com/alexthvest/replipg/internal/application/shop"
	domain "github.com/alexthvest/replipg/internal/modules/shop"
)

const (
	Invoice       = "bank.payment.request.public.v1"
	Transfer      = "bank.transfer.public.v1"
	PaymentStatus = "bank.payment.status.public.v1"
)

type Protocol struct{}

type selector struct {
	Type           string `json:"type"`
	TelegramUserID int64  `json:"telegramUserId"`
}

type invoiceParams struct {
	AccountID int64         `json:"accountId"`
	Payer     selector      `json:"payer"`
	Amount    domain.Amount `json:"amount"`
	Key       string        `json:"idempotencyKey"`
	Comment   string        `json:"comment"`
	ChatID    int64         `json:"chatId"`
}

type transferParams struct {
	AccountID int64         `json:"accountId"`
	Recipient selector      `json:"recipient"`
	Amount    domain.Amount `json:"amount"`
	Key       string        `json:"idempotencyKey"`
}

func (Protocol) Financial(order domain.Order, id, key string) (app.Request, error) {
	if err := order.Quote.Validate(); err != nil {
		return app.Request{}, err
	}
	if !identifier(id) || !identifier(key) || !identifier(string(order.ID)) || order.Quote.TreasuryID > 2147483647 {
		return app.Request{}, errors.New("bank: invalid request identity or treasury")
	}

	q := order.Quote
	request := app.Request{
		ID:           id,
		OrderID:      order.ID,
		FinancialKey: key,
	}
	var params any
	if q.Direction == domain.Buy {
		request.Kind = "invoice"
		request.Method = Invoice
		params = invoiceParams{
			AccountID: q.TreasuryID,
			Payer: selector{
				Type:           "telegram_id",
				TelegramUserID: int64(q.Player),
			},
			Amount:  q.Total,
			Key:     key,
			Comment: "RepliPG order " + string(order.ID),
			ChatID:  q.DeliveryChat,
		}
	} else {
		request.Kind = "payout"
		request.Method = Transfer
		params = transferParams{
			AccountID: q.TreasuryID,
			Recipient: selector{
				Type:           "telegram_id",
				TelegramUserID: int64(q.Player),
			},
			Amount: q.Total,
			Key:    key,
		}
	}

	var err error
	request.Params, err = json.Marshal(params)
	return request, err
}

func (Protocol) Status(order domain.Order, id, paymentID string) (app.Request, error) {
	if !identifier(id) || !identifier(paymentID) || order.Quote.Direction != domain.Buy {
		return app.Request{}, errors.New("bank: invalid payment status identity")
	}
	params, err := json.Marshal(struct {
		RequestID string `json:"requestId"`
	}{RequestID: paymentID})
	return app.Request{
		ID:        id,
		OrderID:   order.ID,
		Kind:      "status",
		Method:    PaymentStatus,
		Params:    params,
		PaymentID: paymentID,
	}, err
}

func (Protocol) Encode(request app.Request) (string, error) {
	if !identifier(request.ID) {
		return "", errors.New("bank: invalid transport ID")
	}
	if request.Method != Invoice && request.Method != Transfer && request.Method != PaymentStatus && request.Method != Balance {
		return "", errors.New("bank: unsupported financial method")
	}
	data, err := json.Marshal(struct {
		Version string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}{
		Version: "2.0",
		ID:      request.ID,
		Method:  request.Method,
		Params:  request.Params,
	})
	if err != nil {
		return "", fmt.Errorf("bank: encode request: %w", err)
	}
	if err = limits(data, false); err != nil {
		return "", err
	}
	return string(data), nil
}

type account struct {
	AccountID      int64   `json:"accountId"`
	TelegramUserID *int64  `json:"telegramUserId,omitempty"`
	Username       *string `json:"username,omitempty"`
}

type transaction struct {
	ID        string        `json:"transactionId"`
	Sender    int64         `json:"senderAccountId"`
	Recipient account       `json:"recipient"`
	Amount    domain.Amount `json:"amount"`
	CreatedAt string        `json:"createdAt"`
}

type payment struct {
	RequestID   string       `json:"requestId"`
	State       string       `json:"state"`
	Reason      string       `json:"reason,omitempty"`
	Transaction *transaction `json:"transaction,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ResponseID validates the envelope before consulting durable correlation.
func ResponseID(text string) (string, error) {
	var envelope struct {
		Version string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *rpcError       `json:"error,omitempty"`
	}
	if err := limits([]byte(text), true); err != nil {
		return "", err
	}
	if err := strict([]byte(text), &envelope); err != nil {
		return "", err
	}
	if envelope.Version != "2.0" || !identifier(envelope.ID) ||
		(envelope.Result == nil) == (envelope.Error == nil) || bytes.Equal(envelope.Result, []byte("null")) {
		return "", errors.New("bank: invalid reply envelope")
	}
	return envelope.ID, nil
}

func (Protocol) Decode(order domain.Order, request app.Request, text string) (app.Evidence, error) {
	id, err := ResponseID(text)
	if err != nil || id != request.ID || request.OrderID != order.ID {
		return app.Evidence{}, errors.New("bank: uncorrelated reply")
	}

	var envelope struct {
		Version string          `json:"jsonrpc"`
		ID      string          `json:"id"`
		Result  json.RawMessage `json:"result,omitempty"`
		Error   *rpcError       `json:"error,omitempty"`
	}
	if err = strict([]byte(text), &envelope); err != nil {
		return app.Evidence{}, err
	}

	evidence := app.Evidence{Response: json.RawMessage(text)}
	if envelope.Error != nil {
		messages := map[int]string{
			-32601: "Method unavailable",
			-32602: "Invalid params",
			-32603: "Internal error",
			-32001: "Request conflict",
			-32003: "Response too large",
			-32002: "Request pending or delivery unknown",
			-32004: "Outcome unknown; reconciliation required",
		}
		if messages[envelope.Error.Code] != envelope.Error.Message {
			return app.Evidence{}, errors.New("bank: undocumented RPC error")
		}

		evidence.ErrorCode = envelope.Error.Code
		evidence.Diagnostic = envelope.Error.Message
		return evidence, nil
	}

	var result struct {
		Data      json.RawMessage `json:"data"`
		Operation *struct {
			ActionID    string `json:"actionId"`
			OperationID string `json:"operationId"`
		} `json:"operation,omitempty"`
	}
	if err = strict(envelope.Result, &result); err != nil {
		return app.Evidence{}, err
	}
	if result.Operation != nil && (request.Method != Invoice ||
		result.Operation.ActionID != Invoice || result.Operation.OperationID == "") {
		return app.Evidence{}, errors.New("bank: invalid operation metadata")
	}

	var proof *transaction
	if request.Method == Transfer {
		proof = &transaction{}
		if err = strict(result.Data, proof); err != nil {
			return app.Evidence{}, err
		}
		evidence.State = "paid"
	} else {
		var payment payment
		if err = strict(result.Data, &payment); err != nil {
			return app.Evidence{}, err
		}
		if !identifier(payment.RequestID) || (request.PaymentID != "" && request.PaymentID != payment.RequestID) {
			return app.Evidence{}, errors.New("bank: mismatched payment request")
		}

		evidence.PaymentID = payment.RequestID
		evidence.State = payment.State
		proof = payment.Transaction
		switch payment.State {
		case "pending", "rejected":
			if proof != nil || payment.Reason != "" {
				return app.Evidence{}, errors.New("bank: unexpected unpaid proof")
			}
		case "failed":
			if proof != nil || payment.Reason != "insufficient_funds" {
				return app.Evidence{}, errors.New("bank: invalid failure reason")
			}
			evidence.Diagnostic = payment.Reason
		case "paid":
			if proof == nil || payment.Reason != "" {
				return app.Evidence{}, errors.New("bank: payment has no transaction proof")
			}
		default:
			return app.Evidence{}, errors.New("bank: unsupported payment state")
		}
	}

	if proof != nil {
		if err = verify(order, request, *proof); err != nil {
			return app.Evidence{}, err
		}

		evidence.TransactionID = proof.ID
		evidence.SenderAccountID = proof.Sender
		evidence.CounterpartyAccountID = proof.Sender
		if order.Quote.Direction == domain.Sell {
			evidence.CounterpartyAccountID = proof.Recipient.AccountID
		}
	}
	return evidence, nil
}

func verify(order domain.Order, request app.Request, proof transaction) error {
	if !identifier(proof.ID) || proof.Sender <= 0 || proof.Sender > 2147483647 ||
		proof.Recipient.AccountID <= 0 || proof.Recipient.AccountID > 2147483647 ||
		proof.Amount != order.Quote.Total || proof.CreatedAt == "" {
		return errors.New("bank: invalid transaction proof")
	}

	if order.Quote.Direction == domain.Buy {
		if proof.Recipient.AccountID != order.Quote.TreasuryID || proof.Sender == order.Quote.TreasuryID ||
			(request.SenderAccountID != 0 && proof.Sender != request.SenderAccountID) {
			return errors.New("bank: mismatched incoming transaction")
		}
	} else if proof.Sender != order.Quote.TreasuryID || proof.Recipient.AccountID == order.Quote.TreasuryID ||
		(request.SenderAccountID != 0 && proof.Recipient.AccountID != request.SenderAccountID) ||
		(proof.Recipient.TelegramUserID != nil && *proof.Recipient.TelegramUserID != int64(order.Quote.Player)) {
		return errors.New("bank: mismatched payout transaction")
	}
	return nil
}

func identifier(value string) bool {
	return utf8.ValidString(value) && len([]rune(value)) >= 1 && len([]rune(value)) <= 128
}

func strict(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("bank: invalid JSON: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("bank: trailing JSON")
	}
	return nil
}

func limits(data []byte, response bool) error {
	if !utf8.Valid(data) || len(data) > 16384 || (response && len(utf16.Encode([]rune(string(data)))) > 4096) {
		return errors.New("bank: envelope exceeds encoding limits")
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := object(decoder, 0); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("bank: trailing JSON")
	}
	return nil
}

func object(decoder *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("bank: JSON nesting exceeds limit")
	}

	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); ok {
		if delimiter != '{' {
			return errors.New("bank: arrays and batches are unsupported")
		}

		keys := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}

			name, ok := key.(string)
			if !ok || keys[name] {
				return errors.New("bank: duplicate object field")
			}
			keys[name] = true
			if err = object(decoder, depth+1); err != nil {
				return err
			}
		}

		_, err = decoder.Token()
		return err
	}
	if depth == 0 {
		return errors.New("bank: envelope must be an object")
	}
	return nil
}
