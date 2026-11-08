// Package content holds one immutable validated item and locale version.
package content

import (
	"fmt"
	"maps"
	"slices"

	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/modules/localization"
)

type Bundle struct {
	definitions map[string]items.Definition
	locales     localization.Catalog
}

func New(definitions map[string]items.Definition, translations map[string]map[string]string) (*Bundle, error) {
	locales, err := localization.New(translations)
	if err != nil {
		return nil, err
	}

	for id, d := range definitions {
		if err = items.Validate(id, d, locales.Lookup); err != nil {
			return nil, err
		}
	}
	return &Bundle{
		definitions: maps.Clone(definitions),
		locales:     locales,
	}, nil
}

func (b *Bundle) Locales() localization.Catalog {
	return b.locales
}

func (b *Bundle) Item(id string) (items.Definition, bool) {
	d, ok := b.definitions[id]
	return d, ok
}

func (b *Bundle) ItemIDs() []string {
	return slices.Sorted(maps.Keys(b.definitions))
}

func (b *Bundle) Capture(id string) (items.Snapshot, error) {
	d, ok := b.definitions[id]
	if !ok {
		return items.Snapshot{}, fmt.Errorf("unknown catalog item %s", id)
	}

	snapshot := items.Snapshot{
		Version:   1,
		CatalogID: id,
		Mode:      d.Mode,
		Rarity:    d.Rarity,
		English: items.Text{
			Name:        b.locales.Text("en", d.Name),
			Description: b.locales.Text("en", d.Description),
		},
		Russian: items.Text{
			Name:        b.locales.Text("ru", d.Name),
			Description: b.locales.Text("ru", d.Description),
		},
	}
	return snapshot, snapshot.Validate()
}

func (b *Bundle) Compatible(previous *Bundle) error {
	for id, next := range b.definitions {
		if old, exists := previous.definitions[id]; exists && old.Mode != next.Mode {
			return fmt.Errorf("item %s: continuously active ownership mode cannot change", id)
		}
	}
	return nil
}
