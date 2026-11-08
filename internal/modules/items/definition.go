// Package items owns catalog validation and immutable definition values.
package items

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

type Definition struct {
	Name        string
	Description string
	Rarity      string
	Mode        Mode
	Components  Components
}

func Validate(id string, d Definition, text func(string, string) (string, bool)) error {
	if d.Mode != Stackable && d.Mode != Individual {
		return fmt.Errorf("item %s: explicit ownership mode required", id)
	}
	if strings.TrimSpace(id) == "" || len(id) > 128 {
		return fmt.Errorf("invalid item ID %q", id)
	}

	switch d.Rarity {
	case "common", "uncommon", "rare", "epic", "legendary":
	default:
		return fmt.Errorf("item %s: unsupported rarity", id)
	}

	for _, language := range []string{"en", "ru"} {
		for _, field := range []struct {
			key   string
			limit int
		}{
			{
				d.Name,
				128,
			},
			{
				d.Description,
				1000,
			},
		} {
			value, ok := text(language, field.key)
			if !ok || !utf8.ValidString(value) || strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > field.limit {
				return fmt.Errorf("item %s: invalid %s reference %q", id, language, field.key)
			}
		}
	}
	return nil
}
