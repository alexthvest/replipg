package views

import (
	"fmt"

	"github.com/alexthvest/replipg/internal/content"
	"github.com/alexthvest/replipg/internal/core"
)

func Items(bundle *content.Bundle, language string, page int) (core.View, int, int) {
	ids := bundle.ItemIDs()
	pages := max(1, (len(ids)+PageSize-1)/PageSize)
	page = max(0, min(page, pages-1))
	catalog := bundle.Locales()
	view := core.View{
		Text: core.Text{
			{
				Text:  Preview(catalog.Text(language, "menu.items.title"), 80),
				Style: core.Bold,
			},
		},
	}
	if len(ids) == 0 {
		view.Text = append(view.Text, core.Span{Text: "\n\n" + Preview(catalog.Text(language, "menu.items.empty"), 160)})
	}

	start := page * PageSize
	for i, id := range ids[start:min(start+PageSize, len(ids))] {
		definition, _ := bundle.Item(id)
		prefix := "\n"
		if i == 0 {
			prefix = "\n\n"
		}

		view.Text = append(view.Text,
			core.Span{Text: fmt.Sprintf("%s%d. %s ", prefix, i+1, Preview(catalog.Text(language, definition.Name), 128))},
			core.Span{
				Text:  "(" + id + ")",
				Style: core.Code,
			},
		)
	}

	view.Text = append(view.Text,
		core.Span{Text: "\n\n" + Preview(catalog.Text(language, "menu.items.page"), 80) + " "},
		core.Span{
			Text:  fmt.Sprintf("%d/%d", page+1, pages),
			Style: core.Code,
		},
	)
	return view, page, pages
}
