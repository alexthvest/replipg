package inventory

import (
	"fmt"
	"strings"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type localizedRenderer struct {
	catalog *localization.Catalog
	locale  localization.Locale
	err     error
}

func (r *localizedRenderer) text(k localization.Key, args localization.Args) string {
	if r.err != nil {
		return ""
	}

	s, e := r.catalog.Text(r.locale, k, args)
	r.err = e
	return s
}
func (r *localizedRenderer) item(id contract.ItemID) localization.ItemText {
	if r.err != nil {
		return localization.ItemText{}
	}

	v, e := r.catalog.Item(r.locale, string(id))
	r.err = e
	return v
}

func (r *localizedRenderer) owner(label string) string {
	clean, fallback := cleanOwnerLabel(label)
	if fallback {
		return r.text("inventory.owner", nil)
	}

	return clean
}

func renderPageLocalized(catalog Catalog, page inventoryPage, label string, controls viewControls, p Presentation, l localization.Locale) (runtimecontract.MessageContent, error) {
	r := localizedRenderer{catalog: p.Catalog, locale: l}
	var text strings.Builder

	text.WriteString(r.text("inventory.title", localization.Args{"owner": localization.String(r.owner(label))}))
	items := page.Current.Items
	if len(items) > inventoryPageSize {
		items = items[:inventoryPageSize]
	}
	if len(items) == 0 {
		text.WriteString(r.text("inventory.empty", nil))
	}
	var buttons []runtimecontract.InlineButton

	for index, owned := range items {
		if _, ok := catalog.Lookup(owned.ItemID); !ok {
			continue
		}

		item := r.item(owned.ItemID)
		fmt.Fprintf(&text, "%d. %s (%d)", index+1, item.Name, owned.Quantity)
		if page.Current.Weapon == owned.ItemID || page.Current.Armor == owned.ItemID {
			text.WriteString(" • " + r.text("inventory.equipped", nil))
		}
		text.WriteByte('\n')
		if index < len(controls.Items) && controls.Items[index] != "" {
			buttons = append(buttons, runtimecontract.InlineButton{Text: fmt.Sprint(index + 1), CallbackData: controls.Items[index]})
		}
	}

	text.WriteString("\n" + r.text("inventory.equipment", nil) + "\n")
	for _, slot := range []struct {
		key    localization.Key
		id     contract.ItemID
		weapon bool
	}{{"inventory.weapon", page.Current.Weapon, true}, {"inventory.armor", page.Current.Armor, false}} {
		label := r.text(slot.key, nil)
		item, ok := catalog.Lookup(slot.id)
		if !ok {
			fmt.Fprintf(&text, "%s: %s\n", label, r.text("inventory.slot_empty", nil))
			continue
		}

		name := r.item(slot.id).Name
		value := item.DefenseBonus
		key := localization.Key("inventory.slot_defense")
		if slot.weapon {
			value = item.AttackBonus
			key = "inventory.slot_attack"
		}
		text.WriteString(r.text(key, localization.Args{"label": localization.String(label), "item": localization.String(name), "value": localization.Integer(value)}))
	}

	text.WriteString(r.text("inventory.bonuses", localization.Args{"attack": localization.Integer(page.Current.Modifiers.Attack), "defense": localization.Integer(page.Current.Modifiers.Defense)}))
	content := runtimecontract.MessageContent{Text: text.String()}
	if len(buttons) > 0 {
		content.Buttons = append(content.Buttons, buttons)
	}
	var nav []runtimecontract.InlineButton

	for _, v := range []struct {
		key   localization.Key
		token string
	}{{"inventory.previous", controls.Previous}, {"inventory.next", controls.Next}} {
		if v.token != "" {
			nav = append(nav, runtimecontract.InlineButton{Text: r.text(v.key, nil), CallbackData: v.token})
		}
	}

	if len(nav) > 0 {
		content.Buttons = append(content.Buttons, nav)
	}
	return content, r.err
}
func renderDetailLocalized(catalog Catalog, snapshot contract.Snapshot, id contract.ItemID, label string, controls viewControls, p Presentation, l localization.Locale) (runtimecontract.MessageContent, error) {
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

	r := localizedRenderer{catalog: p.Catalog, locale: l}
	translated := r.item(id)
	category := r.text(localization.Key("category."+string(item.Category)), nil)
	text := r.text("inventory.detail", localization.Args{"item": localization.String(translated.Name), "owner": localization.String(r.owner(label)), "description": localization.String(translated.Description), "category": localization.String(category), "quantity": localization.Integer(quantity)})
	var actions []runtimecontract.InlineButton

	switch item.Category {
	case contract.CategoryConsumable:
		text += r.text("inventory.healing", localization.Args{"value": localization.Integer(item.HealingHP)})
	case contract.CategoryWeapon, contract.CategoryArmor:
		key := localization.Key("inventory.defense")
		value := item.DefenseBonus
		if item.Category == contract.CategoryWeapon {
			key = "inventory.attack"
			value = item.AttackBonus
		}
		text += r.text(key, localization.Args{"value": localization.Integer(value)})
		equipped := snapshot.Weapon == id || snapshot.Armor == id
		if equipped {
			text += r.text("inventory.equipped", nil)
			if controls.Unequip != "" {
				actions = append(actions, runtimecontract.InlineButton{Text: r.text("inventory.unequip", nil), CallbackData: controls.Unequip})
			}
		} else {
			text += r.text("inventory.unequipped", nil)
			if controls.Equip != "" {
				actions = append(actions, runtimecontract.InlineButton{Text: r.text("inventory.equip", nil), CallbackData: controls.Equip})
			}
		}
	}

	content := runtimecontract.MessageContent{Text: text}
	if len(actions) > 0 {
		content.Buttons = append(content.Buttons, actions)
	}
	if controls.Back != "" {
		content.Buttons = append(content.Buttons, []runtimecontract.InlineButton{{Text: r.text("inventory.back", nil), CallbackData: controls.Back}})
	}
	return content, r.err
}
