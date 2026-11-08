package views

import (
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/localization"
)

func Character(catalog localization.Catalog, language string, c characters.Character) core.View {
	return core.View{
		Text: core.Text{
			{
				Text:  Preview(catalog.Text(language, "menu.character.title"), 80),
				Style: core.Bold,
			},
			{Text: "\n" + Preview(catalog.Text(language, "menu.character.name"), 80) + ": "},
			{
				Text:  c.Name,
				Style: core.Code,
			},
			{Text: "\n" + Preview(catalog.Text(language, "menu.character.id"), 80) + ": "},
			{
				Text:  string(c.ID),
				Style: core.Code,
			},
		},
	}
}
