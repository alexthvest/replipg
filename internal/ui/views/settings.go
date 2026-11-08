// Package views renders literal text and semantic spans independently of Telegram.
package views

import (
	"strings"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/localization"
)

func Preview(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}

func Settings(catalog localization.Catalog, language string) core.View {
	return core.View{
		Text: core.Text{
			{
				Text:  Preview(catalog.Text(language, "menu.settings.title"), 80),
				Style: core.Bold,
			},
			{Text: "\n" + Preview(catalog.Text(language, "menu.settings.language"), 80) + ": "},
			{
				Text:  Preview(catalog.Text(language, "language."+language), 80),
				Style: core.Code,
			},
		},
	}
}
