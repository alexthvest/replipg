package inventory

import (
	"testing"
	"unicode/utf16"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func styledFragment(content runtimecontract.MessageContent, kind runtimecontract.TextEntityKind, text string) bool {
	units := utf16.Encode([]rune(content.Text))
	for _, entity := range content.Entities {
		if entity.Kind == kind && string(utf16.Decode(units[entity.Offset:entity.Offset+entity.Length])) == text {
			return true
		}
	}

	return false
}

func TestInventoryStylingPreservesContentAndActions(t *testing.T) {
	catalog, err := LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	snapshot := contract.Snapshot{Items: []contract.OwnedItem{{ItemID: "stick", Quantity: 1}}, Weapon: "stick", Modifiers: contract.Modifiers{Attack: 1}}
	controls := viewControls{Items: []string{"item"}, Equip: "equip", Unequip: "unequip", Back: "back"}
	owner := "😀<b>Игрок_1`</b>"
	for _, locale := range []localization.Locale{localization.English, localization.Russian} {
		p := Presentation{Catalog: c}
		page, err := renderPageLocalized(catalog, inventoryPage{Current: snapshot}, owner, controls, p, locale)
		if err != nil {
			t.Fatal(err)
		}

		assertRenderedResponse(t, page)
		title := "Inventory"
		if locale == localization.Russian {
			title = "Инвентарь"
		}

		if !styledFragment(page, runtimecontract.TextEntityBold, title) || !styledFragment(page, runtimecontract.TextEntityCode, owner) || !styledFragment(page, runtimecontract.TextEntityCode, "(1)") || page.Buttons[0][0].Text != "1" || page.Buttons[0][0].CallbackData != "item" || page.Buttons[0][0].Style != "" {
			t.Fatalf("invalid page styling: %+v", page)
		}

		for _, equipped := range []bool{true, false} {
			current := snapshot
			if !equipped {
				current.Weapon = ""
			}

			detail, err := renderDetailLocalized(catalog, current, "stick", owner, controls, p, locale)
			if err != nil {
				t.Fatal(err)
			}

			assertRenderedResponse(t, detail)
			item, err := c.Item(locale, "stick")
			if err != nil {
				t.Fatal(err)
			}

			style := runtimecontract.ButtonSuccess
			token := "equip"
			if equipped {
				style, token = "", "unequip"
			}

			if !styledFragment(detail, runtimecontract.TextEntityBold, item.Name) || !styledFragment(detail, runtimecontract.TextEntityItalic, item.Description) || !styledFragment(detail, runtimecontract.TextEntityCode, "(1)") || !styledFragment(detail, runtimecontract.TextEntityCode, "+1") || detail.Buttons[0][0].Style != style || detail.Buttons[0][0].CallbackData != token || detail.Buttons[1][0].Style != "" || detail.Buttons[1][0].CallbackData != "back" {
				t.Fatalf("invalid item styling: %+v", detail)
			}
		}

		empty, err := renderPageLocalized(catalog, inventoryPage{}, "", viewControls{}, p, locale)
		if err != nil {
			t.Fatal(err)
		}

		assertRenderedResponse(t, empty)
		fallback, err := c.Text(locale, "inventory.owner", nil)
		if err != nil || !styledFragment(empty, runtimecontract.TextEntityCode, fallback) || len(empty.Buttons) != 0 {
			t.Fatalf("invalid empty inventory: %+v %v", empty, err)
		}
	}
}
