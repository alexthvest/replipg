package cueassets

import (
	"github.com/alexthvest/replipg/internal/modules/items"
	"os"
	"path/filepath"
	"testing"
)

func copyAssets(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	for _, name := range []string{"items", "locales"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}

		files, err := filepath.Glob(filepath.Join("..", "..", "..", "assets", name, "*.cue"))
		if err != nil {
			t.Fatal(err)
		}

		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if err = os.WriteFile(filepath.Join(root, name, filepath.Base(path)), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

func TestLoadShippedAndInvalidAssets(t *testing.T) {
	root := copyAssets(t)
	bundle, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if d, ok := bundle.Item("coin"); !ok || d.Rarity != "common" {
		t.Fatalf("coin: %+v %v", d, ok)
	}

	if bundle.Locales().Text("ru", "command.ping.message") != "Понг!" {
		t.Fatal("Russian locale missing")
	}

	for _, tc := range []struct {
		path string
		text string
	}{
		{
			"items/bad.cue",
			"package items\ncoin: rarity: \"mythic\"",
		},
		{
			"locales/bad.cue",
			"package locales\nen: \"only.english\": \"text\"",
		},
		{
			"items/bad.cue",
			"package items\ncoin: name: \"missing.key\"",
		},
		{
			"items/bad.cue",
			"invalid syntax {{{",
		},
	} {
		path := filepath.Join(root, tc.path)
		if err = os.WriteFile(path, []byte(tc.text), 0600); err != nil {
			t.Fatal(err)
		}

		if _, err = Load(root); err == nil {
			t.Fatalf("accepted %s", tc.text)
		}

		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	if _, err = Load(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing resources accepted")
	}
}

func TestCaptureComparison(t *testing.T) {
	if same(map[string][]byte{"a": []byte("1")}, map[string][]byte{"b": []byte("1")}) {
		t.Fatal("changed set accepted")
	}

	if same(map[string][]byte{"a": []byte("1")}, map[string][]byte{"a": []byte("2")}) {
		t.Fatal("changed content accepted")
	}
}

func TestComponentAssets(t *testing.T) {
	root := copyAssets(t)
	// Legacy definitions still default to empty even when using a classified item's text.
	legacyPath := filepath.Join(root, "items", "legacy.cue")
	legacy := `package items
legacy: {name: "item.stick.name", description: "item.stick.description", rarity: "common", mode: "individual"}
`
	if err := os.WriteFile(legacyPath, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}

	withLegacy, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}

	definition, _ := withLegacy.Item("legacy")
	if definition.Components != (items.Components{}) {
		t.Fatal("legacy definition acquired inferred components")
	}

	path := filepath.Join(root, "items", "components.cue")
	for _, object := range []string{
		`{consumable: {}, material: {}, weapon: {}}`, `{}`, `{unknown: {}}`,
		`null`, `[]`, `true`, `"weapon"`, `{weapon: null}`, `{weapon: true}`,
		`{weapon: []}`, `{weapon: {damage: 10}}`,
	} {
		if err := os.WriteFile(path, []byte("package items\nlegacy: components: "+object), 0600); err != nil {
			t.Fatal(err)
		}

		loaded, err := Load(root)
		valid := object == `{consumable: {}, material: {}, weapon: {}}` || object == `{}`
		if !valid {
			if err == nil {
				t.Fatalf("invalid startup component assets accepted: %s", object)
			}

			files, err := capture(root)
			if err != nil {
				t.Fatal(err)
			}

			var raw map[string]any
			if err := evaluate(filepath.Join(root, "items"), files, &raw); err == nil {
				t.Fatalf("CUE constraints accepted invalid components: %s", object)
			}
			continue
		}

		if err != nil {
			t.Fatalf("%s: %v", object, err)
		}

		definition, _ := loaded.Item("legacy")
		if definition.Components.Weapon != (object != `{}`) || definition.Components.Material != (object != `{}`) || definition.Components.Consumable != (object != `{}`) {
			t.Fatalf("lost markers: %+v", definition)
		}
	}
}
