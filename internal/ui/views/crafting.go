package views

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/modules/localization"
	"github.com/alexthvest/replipg/internal/ui/menus"
)

// CraftMaterials shows only currently available material entries; selected entries
// remain in the caller's accumulated selection when the page changes.
func CraftMaterials(
	catalog localization.Catalog,
	language string,
	entries []inventory.Entry,
	quantities map[core.ID]int64,
	page int,
) (core.View, []inventory.Entry, int, int) {
	var materials []inventory.Entry
	for _, entry := range entries {
		if entry.Snapshot.Components.Material && entry.Available() > 0 {
			materials = append(materials, entry)
		}
	}
	page, pages, start, end := craftPage(page, len(materials))
	visible := materials[start:end]
	view := craftTitle(catalog, language)
	if len(visible) == 0 {
		view.Text = append(view.Text, core.Span{Text: "\n\n" + Preview(catalog.Text(language, "menu.craft.empty"), 240)})
	}
	for i, entry := range visible {
		if i == 0 {
			view.Text = append(view.Text, core.Span{Text: "\n"})
		}
		view.Text = append(view.Text, core.Span{Text: fmt.Sprintf("\n%d. %s", i+1, Preview(entry.Snapshot.Text(language).Name, 128))})
		if entry.Snapshot.Mode == items.Stackable {
			view.Text = append(view.Text, core.Span{
				Text:  " (" + strconv.FormatInt(entry.Available(), 10) + ")",
				Style: core.Code,
			})
		}
		if quantity := quantities[entry.ID]; quantity > 0 {
			view.Text = append(view.Text, core.Span{Text: " • " + Preview(catalog.Text(language, "menu.craft.selected"), 80) + " × " + strconv.FormatInt(quantity, 10)})
		}
	}
	craftPageLabel(&view, catalog, language, page, pages)
	return view, visible, page, pages
}

func CraftEditor(catalog localization.Catalog, language string, entry inventory.Entry, quantity int64) core.View {
	view := craftTitle(catalog, language)
	view.Text = append(view.Text,
		core.Span{
			Text:  "\n\n" + Preview(entry.Snapshot.Text(language).Name, 128),
			Style: core.Bold,
		},
		core.Span{
			Text:  "\n" + Preview(entry.Snapshot.Text(language).Description, 600),
			Style: core.Italic,
		},
	)
	if entry.Snapshot.Mode == items.Stackable {
		view.Text = append(view.Text, core.Span{
			Text:  "\n\n" + Preview(catalog.Text(language, "menu.craft.quantity"), 80) + ": " + strconv.FormatInt(quantity, 10),
			Style: core.Code,
		})
	}
	view.Text = append(view.Text, core.Span{Text: "\n" + Preview(catalog.Text(language, "menu.craft.available"), 80) + ": " + strconv.FormatInt(entry.Available(), 10)})
	if entry.Available() <= 0 || quantity > entry.Available() {
		view.Text = append(view.Text, core.Span{Text: "\n\n" + Preview(catalog.Text(language, "menu.craft.unavailable"), 320)})
	}
	return view
}

// CraftReview pages an arbitrarily large selection without truncating its binding.
func CraftReview(
	catalog localization.Catalog,
	language string,
	allocations []crafting.Allocation,
	_ string,
	page int,
) (core.View, int, int) {
	page, pages, start, end := craftPage(page, len(allocations))
	view := craftTitle(catalog, language)
	view.Text = append(view.Text, core.Span{
		Text:  "\n\n" + Preview(catalog.Text(language, "menu.craft.review"), 120),
		Style: core.Bold,
	})
	for _, allocation := range allocations[start:end] {
		view.Text = append(view.Text,
			core.Span{Text: "\n" + Preview(allocation.Snapshot.Text(language).Name, 128)},
			core.Span{
				Text:  " × " + strconv.FormatInt(allocation.Quantity, 10),
				Style: core.Code,
			},
		)
	}
	view.Text = append(view.Text, core.Span{Text: "\n\n" + Preview(catalog.Text(language, "menu.craft.loss_warning"), 400)})
	craftPageLabel(&view, catalog, language, page, pages)
	return view, page, pages
}

func CraftStatus(catalog localization.Catalog, language, state string, output *items.Snapshot) core.View {
	state = strings.ReplaceAll(state, "_", "-")
	switch state {
	case "pending", "success", "uncraftable", "unavailable", "technical-failure", "disabled", "cancelled":
	default:
		state = "technical-failure"
	}
	view := craftTitle(catalog, language)
	view.Text = append(view.Text, core.Span{Text: "\n\n" + Preview(catalog.Text(language, "menu.craft."+state), 400)})
	if state == crafting.Success && output != nil {
		view.Text = append(view.Text,
			core.Span{
				Text:  "\n\n" + Preview(output.Text(language).Name, 128) + " × 1",
				Style: core.Bold,
			},
			core.Span{
				Text:  "\n" + Preview(output.Text(language).Description, 600),
				Style: core.Italic,
			},
		)
	}
	return view
}

func craftTitle(catalog localization.Catalog, language string) core.View {
	return core.View{Text: core.Text{{
		Text:  Preview(catalog.Text(language, "menu.craft.title"), 120),
		Style: core.Bold,
	}}}
}

func craftPage(page, count int) (int, int, int, int) {
	pages := max(1, (count+PageSize-1)/PageSize)
	page = max(0, min(page, pages-1))
	start := page * PageSize
	return page, pages, start, min(start+PageSize, count)
}

func craftPageLabel(view *core.View, catalog localization.Catalog, language string, page, pages int) {
	view.Text = append(view.Text, core.Span{Text: fmt.Sprintf("\n\n%s %d/%d", Preview(catalog.Text(language, "menu.inventory.page"), 80), page+1, pages)})
}

// CraftComposition retains missing ingredients using their selected snapshots.
func CraftComposition(
	catalog localization.Catalog,
	language string,
	selection *menus.CraftSelection,
	snapshots map[core.ID]items.Snapshot,
	entries []inventory.Entry,
	page int,
) (core.View, []menus.CraftPick, int, int) {
	picks := selection.Picks()
	page, pages, start, end := craftPage(page, len(picks))
	view := craftTitle(catalog, language)
	if len(picks) == 0 {
		view.Text = append(view.Text, core.Span{Text: "\n\n" + catalog.Text(language, "menu.craft.composition-empty")})
	}
	for i, pick := range picks[start:end] {
		snapshot := snapshots[pick.Entry]
		available := int64(0)
		for _, entry := range entries {
			if entry.ID == pick.Entry && entry.Snapshot == snapshot {
				available = entry.Available()
				break
			}
		}
		text := fmt.Sprintf("\n%d. %s × %d", i+1, Preview(snapshot.Text(language).Name, 128), pick.Quantity)
		if available < pick.Quantity {
			text += fmt.Sprintf(" (%s: %d; %s: %d)", Preview(catalog.Text(language, "menu.craft.required"), 60), pick.Quantity, Preview(catalog.Text(language, "menu.craft.available"), 60), available)
		}
		if i == 0 {
			text = "\n" + text
		}
		view.Text = append(view.Text, core.Span{Text: text})
	}
	craftPageLabel(&view, catalog, language, page, pages)
	return view, picks[start:end], page, pages
}

func CraftAttempt(
	catalog localization.Catalog,
	language string,
	attempt crafting.Attempt,
	page int,
) (core.View, int, int) {
	view := CraftStatus(catalog, language, attempt.State, attempt.Outcome.Item)
	page, pages, start, end := craftPage(page, len(attempt.Allocations))
	key := "menu.craft.composition"
	if attempt.State == crafting.Success {
		key = "menu.craft.spent"
	}
	view.Text = append(view.Text, core.Span{Text: "\n\n" + Preview(catalog.Text(language, key), 80)})
	for _, allocation := range attempt.Allocations[start:end] {
		view.Text = append(view.Text, core.Span{Text: fmt.Sprintf("\n%s × %d", Preview(allocation.Snapshot.Text(language).Name, 128), allocation.Quantity)})
	}
	craftPageLabel(&view, catalog, language, page, pages)
	return view, page, pages
}
