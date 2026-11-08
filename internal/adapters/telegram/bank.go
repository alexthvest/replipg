package telegram

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/adapters/bank"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

const bankSendTimeout = 10 * time.Second

// TrustedBankHandler consumes only original private messages from the numeric
// bank identity. Correlation and proof validation remain application concerns.
func TrustedBankHandler(
	bankID int64,
	handle func(context.Context, string) error,
) func(context.Context, *models.Update) bool {
	return func(ctx context.Context, update *models.Update) bool {
		if bankID <= 0 || handle == nil || update == nil {
			return false
		}

		message := update.Message
		if message == nil || message.From == nil || message.Chat.Type != models.ChatTypePrivate ||
			message.Chat.ID != bankID || message.From.ID != bankID || !message.From.IsBot ||
			message.EditDate != 0 || message.ForwardOrigin != nil || message.SenderChat != nil ||
			message.IsAutomaticForward || message.Text == "" {
			return false
		}
		if _, err := bank.ResponseID(message.Text); err != nil {
			return true
		}

		_ = handle(ctx, message.Text)
		return true
	}
}

func (b *Bot) SendBank(ctx context.Context, bankID int64, text string) error {
	if bankID <= 0 || !utf8.ValidString(text) || len(text) == 0 || len(text) > 16384 {
		return errors.New("bank: invalid transport request")
	}

	sendCtx, cancel := context.WithTimeout(ctx, bankSendTimeout)
	defer cancel()
	_, err := b.client.SendMessage(sendCtx, &bot.SendMessageParams{
		ChatID: bankID,
		Text:   text,
	})
	if err != nil {
		return errors.New("bank: Telegram delivery unknown")
	}
	return nil
}
