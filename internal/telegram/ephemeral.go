package telegram

import (
	"context"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// SendEphemeral sends or edits one restricted group response without fallback.
func (d *Delivery) SendEphemeral(ctx context.Context, req runtimecontract.EphemeralResponse) (runtimecontract.DeliveryReceipt, error) {
	if err := runtimecontract.ValidateResponseAt(req, d.now()); err != nil {
		return runtimecontract.DeliveryReceipt{}, err
	}
	params := publicParams(runtimecontract.PublicResponse{Destination: req.Destination, Content: req.Content})
	if req.EditMessageID > 0 {
		// An empty keyboard removes controls from stale or expired views.
		markup, _ := params.ReplyMarkup.(*models.InlineKeyboardMarkup)
		if markup == nil {
			markup = &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{}}
		}
		err := d.client.call(ctx, true, func(ctx context.Context) error {
			accepted, err := d.client.bot.EditEphemeralMessageText(ctx, &bot.EditEphemeralMessageTextParams{
				ChatID: params.ChatID, ReceiverUserID: int64(req.TargetUserID), EphemeralMessageID: req.EditMessageID,
				Text: params.Text, Entities: params.Entities, ReplyMarkup: markup,
			})
			if err != nil {
				return err
			}
			if !accepted {
				return &APIError{Kind: ErrorIndeterminate}
			}
			return nil
		})
		if err != nil {
			return runtimecontract.DeliveryReceipt{}, err
		}
		return runtimecontract.DeliveryReceipt{ChatID: req.Destination.ChatID, EphemeralMessageID: int64(req.EditMessageID)}, nil
	}
	params.EphemeralMessageParameters = &models.EphemeralMessageParameters{ReceiverUserID: int64(req.TargetUserID)}
	callback := func(auth runtimecontract.CallbackAuthorization) {
		params.EphemeralMessageParameters.CallbackQueryID = auth.CallbackQueryID
		params.EphemeralMessageParameters.ReplaceCallbackQueryMessage = req.ReplaceCallbackMessage
	}
	incoming := func(auth runtimecontract.IncomingEphemeralAuthorization) {
		// The ordinary message ID must stay zero for an ephemeral reply.
		params.ReplyParameters = &models.ReplyParameters{EphemeralMessageID: auth.EphemeralMessageID}
	}
	switch auth := req.Authorization.(type) {
	case runtimecontract.CallbackAuthorization:
		callback(auth)
	case *runtimecontract.CallbackAuthorization:
		callback(*auth)
	case runtimecontract.IncomingEphemeralAuthorization:
		incoming(auth)
	case *runtimecontract.IncomingEphemeralAuthorization:
		incoming(*auth)
	}
	var receipt runtimecontract.DeliveryReceipt
	err := d.client.call(ctx, true, func(ctx context.Context) error {
		message, err := d.client.bot.SendMessage(ctx, params)
		if err != nil {
			return err
		}
		if message == nil || message.ID != 0 || message.EphemeralMessageID < 0 || message.Chat.ID != int64(req.Destination.ChatID) {
			return &APIError{Kind: ErrorIndeterminate}
		}
		receipt = runtimecontract.DeliveryReceipt{ChatID: req.Destination.ChatID, EphemeralMessageID: int64(message.EphemeralMessageID)}
		return nil
	})
	return receipt, err
}
