package item

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/item/contract"
)

//go:embed items.json
var embeddedCatalog []byte

// Catalog stores immutable definitions in version-controlled content order.
type Catalog struct {
	items []contract.Definition
	byID  map[contract.ItemID]contract.Definition
}

// LoadCatalog validates embedded mechanics without runtime side effects.
func LoadCatalog() (Catalog, error) { return ParseCatalog(embeddedCatalog) }

// ParseCatalog rejects malformed, ambiguous or unsupported item content.
func ParseCatalog(data []byte) (Catalog, error) {
	if !utf8.Valid(data) || !strictJSON(data) {
		return Catalog{}, invalidCatalog("content")
	}

	var content struct {
		Items []contract.Definition `json:"items"`
	}

	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&content); err != nil {
		return Catalog{}, invalidCatalog("content")
	}

	if len(content.Items) == 0 {
		return Catalog{}, invalidCatalog("items")
	}

	c := Catalog{byID: make(map[contract.ItemID]contract.Definition)}
	for _, v := range content.Items {
		if err := v.Validate(); err != nil {
			return Catalog{}, invalidCatalog("mechanics")
		}

		if _, ok := c.byID[v.ID]; ok {
			return Catalog{}, invalidCatalog("item_id")
		}

		c.items = append(c.items, v)
		c.byID[v.ID] = v
	}
	return c, nil
}

// Lookup returns a definition value for the stable identity.
func (c Catalog) Lookup(id contract.ItemID) (contract.Definition, bool) {
	v, ok := c.byID[id]
	return v, ok
}

// Items returns an independent slice in content order.
func (c Catalog) Items() []contract.Definition { return slices.Clone(c.items) }

// Effect resolves usable mechanics and returns safe errors for other identities.
func (c Catalog) Effect(id contract.ItemID) (contract.EffectDefinition, error) {
	if err := id.Validate(); err != nil {
		return contract.EffectDefinition{}, err
	}

	d, ok := c.Lookup(id)
	if !ok {
		return contract.EffectDefinition{}, &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "item_id"}
	}

	return d.EffectDefinition()
}

func invalidCatalog(field string) error {
	return &contract.DomainError{Kind: contract.ErrorInvalidCatalog, Field: field}
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
