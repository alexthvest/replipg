package inventory

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const inventoryPageSize = 8

type inventoryPage struct {
	Current contract.Snapshot
	Page    int
	HasNext bool
}
type viewControls struct {
	Items                                []string
	Previous, Next, Back, Equip, Unequip string
}

func ownerLabel(label string) string {
	var clean []rune
	for _, r := range label {
		if !unicode.IsControl(r) && !unicode.Is(unicode.Cf, r) && r != unicode.ReplacementChar {
			clean = append(clean, r)
			if len(clean) == 64 {
				break
			}
		}
	}
	if strings.TrimSpace(string(clean)) == "" {
		return "you"
	}
	return string(clean)
}
func renderPage(catalog Catalog, page inventoryPage, label string, controls viewControls) runtimecontract.MessageContent {
	var text strings.Builder
	fmt.Fprintf(&text, "Inventory • %s\n\n", ownerLabel(label))
	items := page.Current.Items
	if len(items) > inventoryPageSize {
		items = items[:inventoryPageSize]
	}
	if len(items) == 0 {
		text.WriteString("Your inventory is empty.\n")
	}
	var buttons []runtimecontract.InlineButton
	for index, owned := range items {
		item, ok := catalog.Lookup(owned.ItemID)
		if !ok {
			continue
		}
		fmt.Fprintf(&text, "%d. %s (%d)", index+1, item.Name, owned.Quantity)
		if page.Current.Weapon == owned.ItemID || page.Current.Armor == owned.ItemID {
			text.WriteString(" • Equipped")
		}
		text.WriteByte('\n')
		if index < len(controls.Items) && controls.Items[index] != "" {
			buttons = append(buttons, runtimecontract.InlineButton{Text: fmt.Sprint(index + 1), CallbackData: controls.Items[index]})
		}
	}
	text.WriteString("\nEquipment\n")
	renderSlot(&text, catalog, "Weapon", page.Current.Weapon, true)
	renderSlot(&text, catalog, "Armor", page.Current.Armor, false)
	fmt.Fprintf(&text, "Bonuses: +%d attack, +%d defense", page.Current.Modifiers.Attack, page.Current.Modifiers.Defense)
	content := runtimecontract.MessageContent{Text: text.String()}
	if len(buttons) > 0 {
		content.Buttons = append(content.Buttons, buttons)
	}
	var navigation []runtimecontract.InlineButton
	if controls.Previous != "" {
		navigation = append(navigation, runtimecontract.InlineButton{Text: "Previous", CallbackData: controls.Previous})
	}
	if controls.Next != "" {
		navigation = append(navigation, runtimecontract.InlineButton{Text: "Next", CallbackData: controls.Next})
	}
	if len(navigation) > 0 {
		content.Buttons = append(content.Buttons, navigation)
	}
	return content
}
func renderSlot(text *strings.Builder, catalog Catalog, label string, id contract.ItemID, weapon bool) {
	item, ok := catalog.Lookup(id)
	if !ok {
		fmt.Fprintf(text, "%s: Empty\n", label)
		return
	}
	if weapon {
		fmt.Fprintf(text, "%s: %s (+%d attack)\n", label, item.Name, item.AttackBonus)
	} else {
		fmt.Fprintf(text, "%s: %s (+%d defense)\n", label, item.Name, item.DefenseBonus)
	}
}
func renderDetail(catalog Catalog, snapshot contract.Snapshot, id contract.ItemID, label string, controls viewControls) (runtimecontract.MessageContent, error) {
	item, ok := catalog.Lookup(id)
	if !ok {
		return runtimecontract.MessageContent{}, contract.ErrNotFound
	}
	var quantity int64
	for _, owned := range snapshot.Items {
		if owned.ItemID == id {
			quantity = owned.Quantity
			break
		}
	}
	if quantity <= 0 {
		return runtimecontract.MessageContent{}, contract.ErrNotOwned
	}
	var text strings.Builder
	fmt.Fprintf(&text, "%s • %s\n\n%s\n\nCategory: %s\nOwned: %d\n", item.Name, ownerLabel(label), item.Description, item.Category, quantity)
	var actions []runtimecontract.InlineButton
	switch item.Category {
	case contract.CategoryConsumable:
		fmt.Fprintf(&text, "Heals %d HP\nUsable during battle", item.HealingHP)
	case contract.CategoryWeapon, contract.CategoryArmor:
		if item.Category == contract.CategoryWeapon {
			fmt.Fprintf(&text, "Attack bonus: +%d\n", item.AttackBonus)
		} else {
			fmt.Fprintf(&text, "Defense bonus: +%d\n", item.DefenseBonus)
		}
		equipped := snapshot.Weapon == id || snapshot.Armor == id
		if equipped {
			text.WriteString("Equipped")
			if controls.Unequip != "" {
				actions = append(actions, runtimecontract.InlineButton{Text: "Unequip", CallbackData: controls.Unequip})
			}
		} else {
			text.WriteString("Unequipped")
			if controls.Equip != "" {
				actions = append(actions, runtimecontract.InlineButton{Text: "Equip", CallbackData: controls.Equip})
			}
		}
	}
	content := runtimecontract.MessageContent{Text: text.String()}
	if len(actions) > 0 {
		content.Buttons = append(content.Buttons, actions)
	}
	if controls.Back != "" {
		content.Buttons = append(content.Buttons, []runtimecontract.InlineButton{{Text: "Back", CallbackData: controls.Back}})
	}
	return content, nil
}
