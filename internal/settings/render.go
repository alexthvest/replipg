package settings

import (
	"context"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func (h handler) render(ctx context.Context, tx runtimecontract.DBTX, v Snapshot, actor runtimecontract.UserID, admin bool, guidance string, now time.Time) (runtimecontract.MessageContent, error) {
	text := func(key string, args localization.Args) (string, error) {
		return h.catalog.Text(v.Locale, localization.Key(key), args)
	}
	title, err := text("settings.title", nil)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	language, err := text("language."+string(v.Locale), nil)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	line, err := text("settings.language", localization.Args{"language": localization.String(language)})
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	status := "settings.menu_synced"
	if v.Pending() {
		status = "settings.menu_pending"
	}
	state, err := text(status, nil)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	lines := []string{title, line, state}
	if !admin {
		english, err := text("language.en", nil)
		if err != nil {
			return runtimecontract.MessageContent{}, err
		}

		russian, err := text("language.ru", nil)
		if err != nil {
			return runtimecontract.MessageContent{}, err
		}

		lines = append(lines, english+" / "+russian)
	}
	if guidance == "" && !admin {
		guidance = "settings.unavailable"
	}
	if guidance != "" {
		value, err := text(guidance, nil)
		if err != nil {
			return runtimecontract.MessageContent{}, err
		}

		lines = append(lines, value)
	}
	content := runtimecontract.MessageContent{Text: strings.Join(lines, "\n")}
	if admin {
		for _, l := range []localization.Locale{localization.English, localization.Russian} {
			label, err := text("language."+string(l), nil)
			if err != nil {
				return content, err
			}

			data, err := h.store.CreateControl(ctx, tx, v, actor, l, now)
			if err != nil {
				return content, err
			}

			content.Buttons = append(content.Buttons, []runtimecontract.InlineButton{{Text: label, CallbackData: data}})
		}
	}
	return content, nil
}
