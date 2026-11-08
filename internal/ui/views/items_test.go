package views

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/alexthvest/replipg/internal/adapters/cueassets"
	"github.com/alexthvest/replipg/internal/core"
)

func TestItemsPagesReloadAndEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.CopyFS(root, os.DirFS("../../../assets")); err != nil {
		t.Fatal(err)
	}

	files, err := filepath.Glob(filepath.Join(root, "items", "*.cue"))
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range files {
		if filepath.Base(path) != "schema.cue" {
			if err = os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}
	}

	var definitions strings.Builder
	definitions.WriteString("package items\n")
	for i := 12; i >= 0; i-- {
		fmt.Fprintf(&definitions, "item_%02d: {name: \"item.coin.name\", description: \"item.coin.description\", rarity: \"common\", mode: \"stackable\"}\n", i)
	}

	path := filepath.Join(root, "items", "catalog.cue")
	if err = os.WriteFile(path, []byte(definitions.String()), 0600); err != nil {
		t.Fatal(err)
	}

	bundle, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	ids := bundle.ItemIDs()
	if !slices.IsSorted(ids) || len(ids) != 13 {
		t.Fatalf("catalog order: %v", ids)
	}

	ids[0] = "changed"
	if bundle.ItemIDs()[0] != "item_00" {
		t.Fatal("catalog IDs were not detached")
	}

	for page, count := range []int{6, 6, 1} {
		view, actual, pages := Items(bundle, "ru", page)
		text := plain(view.Text)
		if actual != page || pages != 3 || strings.Count(text, "(item_") != count {
			t.Fatalf("page %d: %s", page, text)
		}
		if view.Text[0].Style != core.Bold || view.Text[0].Text != "Предметы" || !strings.Contains(text, "Монета") {
			t.Fatal("missing translated heading/name")
		}
		for i := range count {
			id := fmt.Sprintf("item_%02d", page*PageSize+i)
			want := fmt.Sprintf("%d. Монета (%s)", i+1, id)
			if !strings.Contains(text, want) {
				t.Fatalf("missing %s", id)
			}
			if i > 0 && !strings.Contains(text, "\n"+want) {
				t.Fatal("missing consecutive numbered row")
			}
		}
		if strings.Count(text, "\n\n") != 2 {
			t.Fatal("unexpected blank lines between item rows")
		}
		for _, span := range view.Text[1:] {
			if strings.Contains(span.Text, "Монета") && span.Style != core.Plain {
				t.Fatal("catalog names must use plain text like inventory names")
			}
		}
		if len(utf16.Encode([]rune(text))) > 4096 {
			t.Fatal("page exceeds Telegram text limit")
		}
	}

	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}

	empty, err := cueassets.Load(root)
	if err != nil {
		t.Fatal(err)
	}

	view, page, pages := Items(empty, "en", 2)
	if page != 0 || pages != 1 || !strings.Contains(plain(view.Text), "No items are available.") || len(view.Rows) != 0 {
		t.Fatalf("empty/reloaded catalog: %+v %d/%d", view, page, pages)
	}

	view, page, _ = Items(bundle, "en", -1)
	if page != 0 || !strings.Contains(plain(view.Text), "item_00") {
		t.Fatal("negative page was not clamped")
	}
}
