package content_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
)

func copyAssets(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"items", "locales"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}

		files, err := filepath.Glob(filepath.Join("..", "..", "assets", name, "*.cue"))
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

func replace(t *testing.T, path, old, new string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), old) {
		t.Fatalf("missing replacement %q", old)
	}

	if err = os.WriteFile(path, []byte(strings.ReplaceAll(string(data), old, new)), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotsAndAtomicCompatibility(t *testing.T) {
	root := copyAssets(t)
	original, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := original.Capture("coin")
	if err != nil {
		t.Fatal(err)
	}

	replace(t, filepath.Join(root, "locales", "en.cue"), "A simple coin.", "A changed coin.")
	edited, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if err = edited.Compatible(original); err != nil {
		t.Fatal(err)
	}

	future, err := edited.Capture("coin")
	if err != nil {
		t.Fatal(err)
	}

	if saved.Equal(future) || saved.English.Description != "A simple coin." {
		t.Fatal("snapshot mutated")
	}

	replace(t, filepath.Join(root, "items", "coin.cue"), "stackable", "individual")
	differentMode, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if err = differentMode.Compatible(edited); err == nil {
		t.Fatal("continuous mode changed")
	}

	definitions, err := filepath.Glob(filepath.Join(root, "items", "*.cue"))
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range definitions {
		if filepath.Base(path) == "schema.cue" {
			continue
		}

		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}

	empty, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if err = empty.Compatible(edited); err != nil {
		t.Fatal(err)
	}

	if _, err = empty.Capture("coin"); err == nil {
		t.Fatal("deleted definition captured")
	}

	if err = differentMode.Compatible(empty); err != nil {
		t.Fatal("restored mode rejected")
	}

	if saved.Text("ru").Name != "Монета" {
		t.Fatal("saved Russian text lost after deletion")
	}
}

func TestInvalidModeOrMissingTextNeverLoads(t *testing.T) {
	for _, mode := range []string{"", "equipped"} {
		root := copyAssets(t)
		path := filepath.Join(root, "items", "bad.cue")
		text := "package items\nbad: {name: \"missing\", description: \"missing\", rarity: \"common\""
		if mode != "" {
			text += ", mode: \"" + mode + "\""
		}

		text += "}\n"
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}

		if _, err := cueassets.Load(root); err == nil {
			t.Fatal("invalid item accepted")
		}
	}
}

func TestComponentCaptureIsDetachedAndCoherent(t *testing.T) {
	root := copyAssets(t)
	componentFile := filepath.Join(root, "items", "components.cue")
	if err := os.WriteFile(componentFile, []byte("package items\ncoin: components: {material: {}}"), 0600); err != nil {
		t.Fatal(err)
	}

	original, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	saved, err := original.Capture("coin")
	if err != nil {
		t.Fatal(err)
	}

	definition, _ := original.Item("coin")
	definition.Components.Material = false
	definition.Components.Weapon = true
	unchanged, _ := original.Capture("coin")
	if unchanged != saved || !saved.Components.Material || saved.Components.Weapon {
		t.Fatal("definition access mutated published data")
	}

	if err := os.WriteFile(componentFile, []byte("package items\ncoin: components: {material: {}, consumable: {}}"), 0600); err != nil {
		t.Fatal(err)
	}

	replace(t, filepath.Join(root, "locales", "en.cue"), "A simple coin.", "A changed coin.")
	replace(t, filepath.Join(root, "items", "coin.cue"), `"common"`, `"rare"`)
	edited, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if err := edited.Compatible(original); err != nil {
		t.Fatal(err)
	}

	future, _ := edited.Capture("coin")
	old, _ := original.Capture("coin")
	if old != saved || !future.Components.Consumable || !future.Components.Material || future.Rarity != "rare" || future.English.Description != "A changed coin." {
		t.Fatalf("incoherent captures: %+v %+v", old, future)
	}

	for _, name := range []string{"coin.cue", "components.cue"} {
		if err := os.Remove(filepath.Join(root, "items", name)); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := removed.Capture("coin"); err == nil {
		t.Fatal("removed definition captured")
	}

	if old != saved || !saved.Components.Material || saved.Components.Consumable {
		t.Fatal("saved membership changed after edit/removal")
	}
}
