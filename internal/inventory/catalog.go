package inventory

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
	"github.com/alexthvest/replipg/internal/localization"
)

// Catalog is the inventory-owned read port for immutable item mechanics.
type Catalog interface {
	Lookup(itemcontract.ItemID) (itemcontract.Definition, bool)
	Items() []itemcontract.Definition
	Effect(itemcontract.ItemID) (itemcontract.EffectDefinition, error)
}

//go:embed starter.json
var embeddedStarter []byte

// LoadStarter loads version-controlled starter quantities for the item catalog.
func LoadStarter(catalog Catalog) ([]contract.GrantLine, error) {
	return ParseStarter(embeddedStarter, catalog)
}

// ParseStarter strictly validates inventory-owned starter quantities.
func ParseStarter(data []byte, catalog Catalog) ([]contract.GrantLine, error) {
	if !utf8.Valid(data) || !strictJSON(data) {
		return nil, invalidCatalog("starter")
	}

	var content struct {
		Starter []struct {
			ItemID   itemcontract.ItemID `json:"item_id"`
			Quantity int64               `json:"quantity"`
		} `json:"starter"`
	}

	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&content); err != nil {
		return nil, invalidCatalog("starter")
	}

	lines := make([]contract.GrantLine, 0, len(content.Starter))
	for _, v := range content.Starter {
		lines = append(lines, contract.GrantLine{ItemID: v.ItemID, Quantity: v.Quantity})
	}
	return validateStarter(catalog, lines)
}

func validateStarter(catalog Catalog, lines []contract.GrantLine) ([]contract.GrantLine, error) {
	if nilPort(catalog) {
		return nil, invalidCatalog("catalog")
	}

	seen := make(map[itemcontract.ItemID]bool)
	categories := make(map[itemcontract.Category]bool)
	result := make([]contract.GrantLine, 0, len(lines))
	for _, v := range lines {
		d, ok := catalog.Lookup(v.ItemID)
		if !ok || seen[v.ItemID] || v.Validate() != nil {
			return nil, invalidCatalog("starter")
		}

		seen[v.ItemID] = true
		categories[d.Category] = true
		result = append(result, v)
	}

	if !categories[itemcontract.CategoryWeapon] || !categories[itemcontract.CategoryArmor] || !categories[itemcontract.CategoryConsumable] {
		return nil, invalidCatalog("starter")
	}

	return result, nil
}

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
