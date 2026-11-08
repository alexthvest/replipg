package views

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/modules/localization"
)

const PageSize = 6

func Inventory(
	catalog localization.Catalog,
	language string,
	owner characters.Character,
	entries []inventory.Entry,
	page int,
) (core.View, []inventory.Entry, int, int) {
	pages := max(1, (len(entries)+PageSize-1)/PageSize)
	page = max(0, min(page, pages-1))
	start := page * PageSize
	visible := entries[start:min(start+PageSize, len(entries))]
	view := core.View{
		Text: core.Text{
			{
				Text:  Preview(catalog.Text(language, "menu.inventory.title"), 80),
				Style: core.Bold,
			},
			{Text: " • "},
			{
				Text:  Preview(owner.Name, 160),
				Style: core.Code,
			},
		},
	}
	if len(visible) == 0 {
		view.Text = append(view.Text, core.Span{Text: "\n" + Preview(catalog.Text(language, "menu.inventory.empty"), 160)})
	}

	for i, entry := range visible {
		prefix := "\n"
		if i == 0 {
			prefix = "\n\n"
		}

		view.Text = append(view.Text, core.Span{
			Text: fmt.Sprintf("%s%d. %s", prefix, i+1, Preview(entry.Snapshot.Text(language).Name, 128)),
		})
		if entry.Snapshot.Mode == items.Stackable {
			view.Text = append(view.Text, core.Span{Text: " "}, core.Span{
				Text:  "(" + strconv.FormatInt(entry.Quantity, 10) + ")",
				Style: core.Code,
			})
		}
	}

	pageLabel := Preview(catalog.Text(language, "menu.inventory.page"), 80)
	view.Text = append(view.Text, core.Span{Text: "\n\n" + pageLabel + " "}, core.Span{
		Text:  fmt.Sprintf("%d/%d", page+1, pages),
		Style: core.Code,
	})
	return view, visible, page, pages
}

func Inspection(catalog localization.Catalog, language string, entry inventory.Entry) core.View {
	text := entry.Snapshot.Text(language)
	labels := make([]string, 0, 3)
	for _, key := range entry.Snapshot.Components.Keys() {
		labels = append(labels, Preview(catalog.Text(language, "item.component."+key+".name"), 80))
	}
	classification := strings.Join(labels, ", ")
	if len(labels) == 0 {
		classification = Preview(catalog.Text(language, "item.component.none"), 80)
	}

	view := core.View{
		Text: core.Text{
			{
				Text:  Preview(catalog.Text(language, "menu.item.title"), 80),
				Style: core.Bold,
			},
		},
	}
	fields := []struct {
		key   string
		value string
	}{
		{
			"menu.item.name",
			text.Name,
		},
		{
			"menu.item.description",
			text.Description,
		},
		{
			"menu.item.rarity",
			Preview(catalog.Text(language, "rarity."+entry.Snapshot.Rarity), 80),
		},
		{
			"menu.item.mode",
			Preview(catalog.Text(language, "mode."+string(entry.Snapshot.Mode)), 80),
		},
		{
			"menu.item.components",
			classification,
		},
	}
	if entry.Snapshot.Mode == items.Stackable {
		fields = append(fields, struct {
			key   string
			value string
		}{
			"menu.item.quantity",
			strconv.FormatInt(entry.Quantity, 10),
		})
	} else {
		fields = append(fields, struct {
			key   string
			value string
		}{
			"menu.item.instance",
			string(entry.ID),
		})
	}
	if entry.Reserved > 0 {
		if entry.Snapshot.Mode == items.Individual {
			fields = append(fields, struct {
				key   string
				value string
			}{
				key:   "menu.item.quantity",
				value: strconv.FormatInt(entry.Quantity, 10),
			})
		}

		fields = append(fields,
			struct {
				key   string
				value string
			}{
				key:   "menu.item.reserved",
				value: strconv.FormatInt(entry.Reserved, 10),
			},
			struct {
				key   string
				value string
			}{
				key:   "menu.item.available",
				value: strconv.FormatInt(entry.Available(), 10),
			},
		)
	}

	for _, field := range fields {
		view.Text = append(view.Text, core.Span{Text: "\n" + Preview(catalog.Text(language, field.key), 80) + ": "}, core.Span{
			Text:  field.value,
			Style: core.Code,
		})
	}
	return view
}
