package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Delivery maps shared response contracts to the pinned Telegram client.
type Delivery struct {
	client *Client
	now    func() time.Time
}

// NewDelivery reuses the initialized client and an injectable clock.
func NewDelivery(client *Client, now func() time.Time) *Delivery {
	if now == nil {
		now = time.Now
	}
	return &Delivery{client: client, now: now}
}

// SendPublic validates and attempts one group-visible message without retries.
func (d *Delivery) SendPublic(ctx context.Context, req runtimecontract.PublicResponse) (runtimecontract.DeliveryReceipt, error) {
	if err := runtimecontract.ValidateResponse(req); err != nil {
		return runtimecontract.DeliveryReceipt{}, err
	}
	params := publicParams(req)
	var receipt runtimecontract.DeliveryReceipt
	err := d.client.call(ctx, true, func(ctx context.Context) error {
		message, err := d.client.bot.SendMessage(ctx, params)
		if err != nil {
			return err
		}
		if message == nil || message.ID <= 0 || message.Chat.ID != int64(req.Destination.ChatID) || message.EphemeralMessageID != 0 {
			return &APIError{Kind: ErrorIndeterminate}
		}
		receipt = runtimecontract.DeliveryReceipt{ChatID: req.Destination.ChatID, MessageID: int64(message.ID)}
		return nil
	})
	return receipt, err
}

func publicParams(req runtimecontract.PublicResponse) *bot.SendMessageParams {
	params := &bot.SendMessageParams{ChatID: int64(req.Destination.ChatID), Text: req.Content.Text}
	for _, entity := range req.Content.Entities {
		params.Entities = append(params.Entities, models.MessageEntity{Type: models.MessageEntityType(entity.Kind), Offset: entity.Offset, Length: entity.Length, URL: entity.URL})
	}
	if len(req.Content.Buttons) > 0 {
		markup := &models.InlineKeyboardMarkup{InlineKeyboard: make([][]models.InlineKeyboardButton, len(req.Content.Buttons))}
		for i, row := range req.Content.Buttons {
			markup.InlineKeyboard[i] = make([]models.InlineKeyboardButton, len(row))
			for j, button := range row {
				markup.InlineKeyboard[i][j] = models.InlineKeyboardButton{Text: button.Text, CallbackData: button.CallbackData}
			}
		}
		params.ReplyMarkup = markup
	}
	return params
}

// AcknowledgeCallback acknowledges an interaction without text or a chat message.
func (d *Delivery) AcknowledgeCallback(ctx context.Context, req runtimecontract.CallbackAcknowledgement) error {
	if err := runtimecontract.ValidateResponse(req); err != nil {
		return err
	}
	return d.client.call(ctx, true, func(ctx context.Context) error {
		accepted, err := d.client.bot.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: req.CallbackQueryID})
		if err != nil {
			return err
		}
		if !accepted {
			return &APIError{Kind: ErrorIndeterminate}
		}
		return nil
	})
}

// Outcome translates one send result into content-free durable-work evidence.
// Unrecognized errors are indeterminate, so they never authorize a blind resend.
func Outcome(receipt runtimecontract.DeliveryReceipt, err error) runtimecontract.DeliveryOutcome {
	if err == nil {
		return runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryAccepted, Receipt: &receipt}
	}
	outcome := runtimecontract.DeliveryOutcome{Kind: runtimecontract.DeliveryIndeterminate, ReasonCode: string(ErrorIndeterminate)}
	var validation *runtimecontract.ValidationError
	if errors.As(err, &validation) {
		outcome.Kind = runtimecontract.DeliveryPermanentFailure
		outcome.ReasonCode = string(ErrorValidation)
		return outcome
	}
	var safe *APIError
	if !errors.As(err, &safe) {
		return outcome
	}
	switch safe.Kind {
	case ErrorValidation, ErrorAuthorization, ErrorConflict, ErrorPermanent:
		outcome.Kind = runtimecontract.DeliveryPermanentFailure
	case ErrorRateLimit, ErrorTransient:
		outcome.Kind = runtimecontract.DeliveryRetryableFailure
		outcome.RetryAfter = safe.RetryAfter
	case ErrorCancellation:
		outcome.Kind = runtimecontract.DeliveryCancelled
	case ErrorIndeterminate:
	default:
		return outcome
	}
	outcome.ReasonCode = string(safe.Kind)
	return outcome
}
