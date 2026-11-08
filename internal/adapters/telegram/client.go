package telegram

import (
	"errors"
	"net/http"
	"time"

	"github.com/go-telegram/bot"
)

// newClient avoids logging library errors, which may contain credentialed URLs.
func newClient(token string, options ...bot.Option) (*bot.Bot, error) {
	options = append([]bot.Option{
		bot.WithHTTPClient(time.Minute, &http.Client{Timeout: time.Minute}),
		bot.WithErrorsHandler(func(error) {}),
	}, options...)

	client, err := bot.New(token, options...)
	if err != nil {
		return nil, errors.New("initialize Telegram: check token and network connectivity")
	}
	return client, nil
}
