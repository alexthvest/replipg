package cueassets

import (
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
