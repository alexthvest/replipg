package items

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Components records marker presence without reference-bearing state.
type Components struct {
	Consumable bool
	Material   bool
	Weapon     bool
}

func (c Components) Has(key string) bool {
	switch key {
	case "consumable":
		return c.Consumable
	case "material":
		return c.Material
	case "weapon":
		return c.Weapon
	default:
		return false
	}
}

// Keys returns a detached list in canonical component-key order.
func (c Components) Keys() []string {
	var keys []string
	for _, key := range []string{"consumable", "material", "weapon"} {
		if c.Has(key) {
			keys = append(keys, key)
		}
	}
	return keys
}

func (c Components) MarshalJSON() ([]byte, error) {
	var result bytes.Buffer
	result.WriteByte('{')
	for i, key := range c.Keys() {
		if i > 0 {
			result.WriteByte(',')
		}

		fmt.Fprintf(&result, "%q:{}", key)
	}

	result.WriteByte('}')
	return result.Bytes(), nil
}

func (c *Components) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		return fmt.Errorf("components: object required")
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("components: %w", err)
	}

	var next Components
	for key, value := range fields {
		var marker map[string]json.RawMessage
		value = bytes.TrimSpace(value)
		if len(value) == 0 || value[0] != '{' {
			return fmt.Errorf("component %s: empty object required", key)
		}

		if err := json.Unmarshal(value, &marker); err != nil || len(marker) != 0 {
			return fmt.Errorf("component %s: empty object required", key)
		}

		switch key {
		case "consumable":
			next.Consumable = true
		case "material":
			next.Material = true
		case "weapon":
			next.Weapon = true
		default:
			return fmt.Errorf("component %s: unsupported type", key)
		}
	}

	*c = next
	return nil
}
