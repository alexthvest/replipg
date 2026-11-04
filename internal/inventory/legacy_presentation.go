package inventory

import (
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/localization"
)

// legacyEnglishCatalog preserves English entrypoints while leaving explicitly
// supplied custom item text and the retained seed definitions untouched.
func legacyEnglishCatalog(catalog Catalog) Catalog {
	source, err := LoadCatalog()
	if err != nil {
		return catalog
	}
	translations, err := localization.Load()
	if err != nil {
		return catalog
	}
	copy := catalog
	copy.byID = make(map[contract.ItemID]ItemDefinition, len(catalog.byID))
	for id, item := range catalog.byID {
		if seed, ok := source.Lookup(id); ok && seed.Name == item.Name && seed.Description == item.Description {
			if text, err := translations.Item(localization.English, string(id)); err == nil {
				item.Name = text.Name
				item.Description = text.Description
			}
		}
		copy.byID[id] = item
	}
	return copy
}
