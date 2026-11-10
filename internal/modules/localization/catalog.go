// Package localization owns supported languages and translation validation.
package localization

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"unicode/utf8"
)

type Catalog struct {
	values map[string]map[string]string
}

var baselineKeys = []string{
	"command.replipg.usage",
	"command.replipg.unavailable",
	"command.replipg.balance",
	"command.items.usage",
	"command.items.given",
	"menu.items.title",
	"menu.items.empty",
	"menu.items.page",
	"menu.character.title",
	"menu.character.name",
	"menu.character.id",
	"error.name",
	"command.ping.message",
	"command.admin.usage",
	"command.admin.updated",
	"menu.settings.title",
	"menu.settings.language",
	"language.en",
	"language.ru",
	"error.denied",
	"error.invalid_menu",
	"error.persistence",
	"rarity.common",
	"rarity.uncommon",
	"rarity.rare",
	"rarity.epic",
	"rarity.legendary",
}

func New(values map[string]map[string]string) (Catalog, error) {
	if len(values) != 2 {
		return Catalog{}, errors.New("locales: exactly English and Russian required")
	}

	en, enOK := values["en"]
	ru, ruOK := values["ru"]
	if !enOK || !ruOK || len(en) != len(ru) {
		return Catalog{}, errors.New("locales: English/Russian key parity required")
	}

	for _, key := range append(append(append(append([]string{}, baselineKeys...), inventoryKeys...), shopKeys...), craftingKeys...) {
		if _, ok := en[key]; !ok {
			return Catalog{}, fmt.Errorf("locales: missing required key %s", key)
		}
	}

	for key, value := range en {
		other, ok := ru[key]
		if !ok {
			return Catalog{}, fmt.Errorf("locales: missing Russian key %s", key)
		}

		for _, text := range []string{value, other} {
			if !strings.HasPrefix(key, "item.") {
				if err := validateTemplate(key, text); err != nil {
					return Catalog{}, err
				}
			}

			if !utf8.ValidString(text) || strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > 1024 {
				return Catalog{}, fmt.Errorf("locales: invalid text for %s", key)
			}
		}
	}
	return Catalog{
		values: map[string]map[string]string{
			"en": maps.Clone(en),
			"ru": maps.Clone(ru),
		},
	}, nil
}

func (c Catalog) Lookup(language, key string) (string, bool) {
	value, ok := c.values[language][key]
	return value, ok
}

func (c Catalog) Text(language, key string) string {
	value, _ := c.Lookup(language, key)
	return value
}
