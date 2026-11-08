package items

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestComponentsCombinations(t *testing.T) {
	for mask := range 8 {
		original := Components{
			Consumable: mask&1 != 0,
			Material:   mask&2 != 0,
			Weapon:     mask&4 != 0,
		}

		data, err := json.Marshal(original)
		if err != nil {
			t.Fatal(err)
		}

		var decoded Components
		if err := json.Unmarshal(data, &decoded); err != nil || decoded != original {
			t.Fatalf("%s: %+v %v", data, decoded, err)
		}

		if decoded.Has("unknown") {
			t.Fatal("unknown membership")
		}

		copy := decoded
		copy.Weapon = !copy.Weapon
		if copy == decoded || decoded != original {
			t.Fatal("component copy aliased")
		}

		keys := decoded.Keys()
		if len(keys) > 0 {
			keys[0] = "changed"
			if reflect.DeepEqual(keys, decoded.Keys()) {
				t.Fatal("enumeration aliased")
			}
		}
	}
}

func TestComponentsStrictDecodeAndReset(t *testing.T) {
	original := Components{Material: true}
	for _, input := range []string{
		`null`, `[]`, `1`, `true`, `"weapon"`,
		`{"unknown":{}}`, `{"weapon":{"damage":10}}`,
		`{"weapon":null}`, `{"weapon":[]}`, `{"weapon":true}`,
		`{"material":{},"weapon":{"x":{}}}`,
	} {
		value := original
		if err := json.Unmarshal([]byte(input), &value); err == nil || value != original {
			t.Fatalf("invalid decode changed value: %s %+v %v", input, value, err)
		}
	}

	if err := json.Unmarshal([]byte(`{}`), &original); err != nil || original != (Components{}) {
		t.Fatalf("reset: %+v %v", original, err)
	}
}
