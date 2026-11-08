package telegram

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/go-telegram/bot"
)

func TestEditClassifications(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want menus.EditError
	}{
		{
			err:  &bot.TooManyRequestsError{RetryAfter: 9},
			want: menus.EditError{RetryAfter: 9 * time.Second},
		},
		{
			err:  fmt.Errorf("%w: message is not modified", bot.ErrorBadRequest),
			want: menus.EditError{NotModified: true},
		},
		{
			err:  fmt.Errorf("%w: message to edit not found", bot.ErrorBadRequest),
			want: menus.EditError{Permanent: true},
		},
		{
			err:  bot.ErrorForbidden,
			want: menus.EditError{Permanent: true},
		},
		{err: errors.New("temporary network failure")},
	} {
		if got := menus.Classify(classifyEdit(tc.err)); got != tc.want {
			t.Errorf("%v: %+v, want %+v", tc.err, got, tc.want)
		}
	}
}
