package telegram

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

type Bot struct {
	client     *bot.Bot
	httpClient *http.Client
	identity   models.User
	handler    func(context.Context, *models.Update)
}

func New(ctx context.Context, token string, logger *slog.Logger) (*Bot, error) {
	if logger == nil {
		return nil, errors.New("telegram: logger required")
	}

	httpClient := &http.Client{
		Timeout:   time.Minute,
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
	}
	b := &Bot{httpClient: httpClient}
	ready := false
	defer func() {
		if !ready {
			httpClient.CloseIdleConnections()
		}
	}()

	client, err := newClient(
		token,
		bot.WithSkipGetMe(),
		bot.WithHTTPClient(30*time.Second, httpClient),
		bot.WithWorkers(1),
		bot.WithNotAsyncHandlers(),
		bot.WithUpdatesChannelCap(128),
		bot.WithAllowedUpdates(bot.AllowedUpdates{
			"message",
			"callback_query",
		}),
		bot.WithErrorsHandler(func(error) {
			logger.Error("Telegram polling failed")
		}),
		bot.WithDefaultHandler(func(ctx context.Context, _ *bot.Bot, u *models.Update) {
			if b.handler != nil {
				b.handler(ctx, u)
			}
		}),
	)
	if err != nil {
		return nil, err
	}

	b.client = client
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	identity, err := client.GetMe(initCtx)
	if err != nil {
		return nil, errors.New("initialize Telegram: check token and network connectivity")
	}
	if identity.ID <= 0 || identity.Username == "" {
		return nil, errors.New("initialize Telegram: invalid bot identity")
	}

	b.identity = *identity
	ready = true
	return b, nil
}

func (b *Bot) Close() error {
	b.httpClient.CloseIdleConnections()
	return nil
}

func (b *Bot) Identity() (int64, string) {
	return b.identity.ID, b.identity.Username
}

func (b *Bot) Start(intake, delivery context.Context, router *Router) {
	b.handler = func(ctx context.Context, u *models.Update) {
		if ctx.Err() == nil {
			router.Handle(delivery, u)
		}
	}
	b.client.Start(intake)
}

func retry(ctx context.Context, call func(context.Context) error) error {
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := call(ctx)
		if err == nil {
			return nil
		}

		delay := time.Duration(attempt+1) * 100 * time.Millisecond
		var rate *bot.TooManyRequestsError
		var network net.Error
		if errors.As(err, &rate) {
			delay = time.Duration(rate.RetryAfter) * time.Second
		} else if !errors.As(err, &network) {
			return errors.New("telegram request rejected")
		}

		if attempt == 2 {
			return errors.New("telegram retries exhausted")
		}

		timer := time.NewTimer(delay)

		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return errors.New("telegram retries exhausted")
}

func (b *Bot) Send(ctx context.Context, group core.GroupID, view core.View) (int, error) {
	text, spans, err := entities(view.Text)
	if err != nil {
		return 0, err
	}

	markup, err := keyboard(view.Rows)
	if err != nil {
		return 0, err
	}

	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var message *models.Message
	err = retry(sendCtx, func(ctx context.Context) error {
		var sendErr error
		message, sendErr = b.client.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:      int64(group),
			Text:        text,
			Entities:    spans,
			ReplyMarkup: markup,
		})
		return sendErr
	})
	if err != nil {
		return 0, err
	}
	return message.ID, nil
}

func (b *Bot) Edit(ctx context.Context, group core.GroupID, id int, view core.View) error {
	text, spans, err := entities(view.Text)
	if err != nil {
		return err
	}

	markup, err := keyboard(view.Rows)
	if err != nil {
		return err
	}

	editCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = b.client.EditMessageText(editCtx, &bot.EditMessageTextParams{
		ChatID:      int64(group),
		MessageID:   id,
		Text:        text,
		Entities:    spans,
		ReplyMarkup: markup,
	})
	return classifyEdit(err)
}

func classifyEdit(err error) error {
	if err == nil {
		return nil
	}

	var rate *bot.TooManyRequestsError
	if errors.As(err, &rate) {
		return &menus.EditError{RetryAfter: time.Duration(rate.RetryAfter) * time.Second}
	}
	text := strings.ToLower(err.Error())
	if errors.Is(err, bot.ErrorBadRequest) && strings.Contains(text, "message is not modified") {
		return &menus.EditError{NotModified: true}
	}
	if errors.Is(err, bot.ErrorForbidden) || errors.Is(err, bot.ErrorNotFound) ||
		(errors.Is(err, bot.ErrorBadRequest) && (strings.Contains(text, "message to edit not found") || strings.Contains(text, "message can't be edited") || strings.Contains(text, "chat not found"))) {
		return &menus.EditError{Permanent: true}
	}
	return &menus.EditError{}
}

func (b *Bot) Ack(ctx context.Context, id, text string) error {
	ackCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return retry(ackCtx, func(ctx context.Context) error {
		_, err := b.client.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: id,
			Text:            text,
		})
		return err
	})
}
