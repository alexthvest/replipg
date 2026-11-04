package inventory

import (
	"strings"
	"unicode"

	"github.com/alexthvest/replipg/internal/inventory/contract"
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

func cleanOwnerLabel(label string) (string, bool) {
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
		return "", true
	}

	return string(clean), false
}
