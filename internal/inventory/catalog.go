package inventory

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/localization"
)

// Seed tuning: Stick adds 1 attack, Cloth armor adds 1 defense and Healing
// potion heals 10 HP. The starter grants 1 weapon, 1 armor, 3 potions and 10
// ordinary inert Coins. Starter equipment is owned, never automatically equipped.
// These are version-controlled balances, independent of presentation mockups.
//
//go:embed items.json
var embeddedCatalog []byte

// ItemDefinition describes fungible copies and keeps mechanics separate from
// character base attributes. Consumables always use fixed self-healing.
type ItemDefinition struct {
	ID           contract.ItemID   `json:"id"`
	Category     contract.Category `json:"category"`
	AttackBonus  int64             `json:"attack_bonus"`
	DefenseBonus int64             `json:"defense_bonus"`
	HealingHP    int64             `json:"healing_hp"`
}

// Catalog contains validated immutable definitions and starter quantities.
// Lookup returns values; slice accessors return independent copies.
type Catalog struct {
	items   []ItemDefinition
	byID    map[contract.ItemID]ItemDefinition
	starter []contract.GrantLine
}

type catalogContent struct {
	Items []struct {
		ItemDefinition
		Effect contract.EffectKind `json:"effect"`
	} `json:"items"`
	Starter []struct {
		ItemID   contract.ItemID `json:"item_id"`
		Quantity int64           `json:"quantity"`
	} `json:"starter"`
}

// LoadCatalog validates the embedded version-controlled content without I/O.
func LoadCatalog() (Catalog, error) { return ParseCatalog(embeddedCatalog) }

// ParseCatalog strictly validates content before exposing any definitions.
// Errors identify only a fixed field name and never echo supplied content.
func ParseCatalog(data []byte) (Catalog, error) {
	if !utf8.Valid(data) || !strictJSON(data) {
		return Catalog{}, invalidCatalog("content")
	}
	var content catalogContent
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&content); err != nil {
		return Catalog{}, invalidCatalog("content")
	}
	catalog := Catalog{byID: make(map[contract.ItemID]ItemDefinition)}
	for _, item := range content.Items {
		definition := item.ItemDefinition
		if definition.ID.Validate() != nil {
			return Catalog{}, invalidCatalog("item_id")
		}
		if _, exists := catalog.byID[definition.ID]; exists {
			return Catalog{}, invalidCatalog("item_id")
		}
		if !validMechanics(definition, item.Effect) {
			return Catalog{}, invalidCatalog("mechanics")
		}
		catalog.byID[definition.ID] = definition
		catalog.items = append(catalog.items, definition)
	}
	seen := make(map[contract.ItemID]bool)
	categories := make(map[contract.Category]bool)
	for _, line := range content.Starter {
		definition, exists := catalog.byID[line.ItemID]
		if !exists || seen[line.ItemID] || line.Quantity <= 0 {
			return Catalog{}, invalidCatalog("starter")
		}
		seen[line.ItemID] = true
		categories[definition.Category] = true
		catalog.starter = append(catalog.starter, contract.GrantLine{ItemID: line.ItemID, Quantity: line.Quantity})
	}
	if !categories[contract.CategoryWeapon] || !categories[contract.CategoryArmor] || !categories[contract.CategoryConsumable] {
		return Catalog{}, invalidCatalog("starter")
	}
	return catalog, nil
}

// Lookup resolves a stable identity to an independent definition value.
func (c Catalog) Lookup(id contract.ItemID) (ItemDefinition, bool) {
	item, ok := c.byID[id]
	return item, ok
}

// Items lists definitions in version-controlled content order.
func (c Catalog) Items() []ItemDefinition { return slices.Clone(c.items) }

// Starter lists positive starter quantities without equipment assignments.
func (c Catalog) Starter() []contract.GrantLine { return slices.Clone(c.starter) }

func invalidCatalog(field string) error {
	return &contract.DomainError{Kind: contract.ErrorInvalidCatalog, Field: field}
}

func validCatalogText(text string, limit int) bool {
	if strings.TrimSpace(text) == "" || utf8.RuneCountInString(text) > limit {
		return false
	}
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return false
		}
	}
	return true
}

func validMechanics(item ItemDefinition, effect contract.EffectKind) bool {
	switch item.Category {
	case contract.CategoryConsumable:
		return item.HealingHP > 0 && item.AttackBonus == 0 && item.DefenseBonus == 0 && effect == contract.EffectHealSelfFixedHP
	case contract.CategoryWeapon:
		return item.AttackBonus >= 0 && item.DefenseBonus == 0 && item.HealingHP == 0 && effect == ""
	case contract.CategoryArmor:
		return item.DefenseBonus >= 0 && item.AttackBonus == 0 && item.HealingHP == 0 && effect == ""
	case contract.CategoryInert:
		return item.AttackBonus == 0 && item.DefenseBonus == 0 && item.HealingHP == 0 && effect == ""
	default:
		return false
	}
}

// encoding/json otherwise accepts duplicate fields, null scalar values and
// case-insensitive field names. Reject those before typed unknown-field and
// integer checks. Catalog schema keys are exclusively lowercase ASCII names.
func strictJSON(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if !scanJSONValue(decoder, 0) {
		return false
	}
	_, err := decoder.Token()
	return errors.Is(err, io.EOF)
}

func scanJSONValue(decoder *json.Decoder, depth int) bool {
	if depth > 8 {
		return false
	}
	token, err := decoder.Token()
	if err != nil || token == nil {
		return false
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return true
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok || seen[key] || !validJSONKey(key) {
				return false
			}
			seen[key] = true
			if !scanJSONValue(decoder, depth+1) {
				return false
			}
		}
	case '[':
		for decoder.More() {
			if !scanJSONValue(decoder, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	closing, err := decoder.Token()
	return err == nil && ((delimiter == '{' && closing == json.Delim('}')) || (delimiter == '[' && closing == json.Delim(']')))
}

func validJSONKey(key string) bool {
	if key == "" {
		return false
	}
	for i := range len(key) {
		if (key[i] < 'a' || key[i] > 'z') && key[i] != '_' {
			return false
		}
	}
	return true
}

// ValidateItemText applies the existing seed catalog text policy to translated text.
func ValidateItemText(text localization.ItemText) error {
	if !validCatalogText(text.Name, 80) || !validCatalogText(text.Description, 400) {
		return invalidCatalog("text")
	}
	return nil
}
