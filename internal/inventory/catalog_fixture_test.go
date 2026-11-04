package inventory

import (
	"encoding/json"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/item"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
)

var embeddedCatalog = []byte(`{
  "items": [
    {"id":"coin","category":"inert"},
    {"id":"stick","category":"weapon","attack_bonus":1},
    {"id":"cloth_armor","category":"armor","defense_bonus":1},
    {"id":"healing_potion","category":"consumable","healing_hp":10,"effect":"heal_self_fixed_hp"}
  ],
  "starter": [
    {"item_id":"coin","quantity":10},
    {"item_id":"stick","quantity":1},
    {"item_id":"cloth_armor","quantity":1},
    {"item_id":"healing_potion","quantity":3}
  ]
}
`)

type fixtureCatalog struct {
	items   []itemcontract.Definition
	byID    map[itemcontract.ItemID]itemcontract.Definition
	starter []contract.GrantLine
}

func (c fixtureCatalog) Items() []itemcontract.Definition {
	return append([]itemcontract.Definition(nil), c.items...)
}

func (c fixtureCatalog) Lookup(id itemcontract.ItemID) (itemcontract.Definition, bool) {
	d, ok := c.byID[id]
	return d, ok
}

func (c fixtureCatalog) Starter() []contract.GrantLine {
	return append([]contract.GrantLine(nil), c.starter...)
}

func (c fixtureCatalog) Effect(id itemcontract.ItemID) (itemcontract.EffectDefinition, error) {
	d, ok := c.Lookup(id)
	if !ok {
		return itemcontract.EffectDefinition{}, itemcontract.ErrInvalidInput
	}

	return d.EffectDefinition()
}

func LoadCatalog() (fixtureCatalog, error) { return ParseCatalog(embeddedCatalog) }

func ParseCatalog(data []byte) (fixtureCatalog, error) {
	if !strictJSON(data) {
		return fixtureCatalog{}, invalidCatalog("content")
	}

	var root map[string]json.RawMessage

	if json.Unmarshal(data, &root) != nil || len(root) != 2 || root["items"] == nil || root["starter"] == nil {
		return fixtureCatalog{}, invalidCatalog("content")
	}

	defs, _ := json.Marshal(map[string]json.RawMessage{"items": root["items"]})
	c, err := item.ParseCatalog(defs)
	if err != nil {
		return fixtureCatalog{}, inventoryItemError(err)
	}

	seed, _ := json.Marshal(map[string]json.RawMessage{"starter": root["starter"]})
	starter, err := ParseStarter(seed, c)
	if err != nil {
		return fixtureCatalog{}, err
	}

	f := fixtureCatalog{items: c.Items(), byID: make(map[itemcontract.ItemID]itemcontract.Definition), starter: starter}
	for _, d := range f.items {
		f.byID[d.ID] = d
	}
	return f, nil
}
