package cueassets

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/content"
	"github.com/alexthvest/replipg/internal/modules/items"
)

func shopAssets(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, "shop")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}

	schema, err := os.ReadFile("../../../assets/shop/schema.cue")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "schema.cue"), schema, 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWatchShopDirectoryLifecycleAndCoherentBundles(t *testing.T) {
	root := copyAssets(t)
	published := make(chan *content.Bundle, 32)
	reports := make(chan error, 32)
	watcher, err := Watch(context.Background(), root, 5*time.Millisecond, func(_ context.Context, b *content.Bundle) error {
		published <- b
		return nil
	}, func(err error) {
		reports <- err
	})
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Stop()

	initial := receive(t, published)
	if len(initial.Offers()) != 0 {
		t.Fatal("missing directory enabled offers")
	}

	// Construct outside the watched root, then atomically publish the package.
	staging := t.TempDir()
	staged := shopAssets(t, staging)
	writeAsset(t, filepath.Join(staged, "bread.cue"), "package shop\nbread: {mode: \"stackable\", buyPrice: \"10\"}\n")
	dir := filepath.Join(root, "shop")
	if err = os.Rename(staged, dir); err != nil {
		t.Fatal(err)
	}
	withShop := receive(t, published)
	if offer, ok := withShop.Offer("bread", items.Stackable); !ok || offer.BuyPrice != "10" {
		t.Fatalf("directory creation missed: %+v", offer)
	}

	writeAsset(t, filepath.Join(dir, "bread.cue"), "package shop\nbread: {mode: \"stackable\", buyPrice: \"0\"}\n")
	_ = receive(t, reports)
	if offer, _ := withShop.Offer("bread", items.Stackable); offer.BuyPrice != "10" {
		t.Fatal("invalid candidate mutated prior bundle")
	}
	select {
	case <-published:
		t.Fatal("invalid offer was published")
	default:
	}

	writeAsset(t, filepath.Join(root, "locales", "invalid.cue"), "invalid syntax {{{")
	_ = receive(t, reports)
	writeAsset(t, filepath.Join(dir, "bread.cue"), "package shop\nbread: {mode: \"stackable\", buyPrice: \"12\"}\n")
	_ = receive(t, reports)
	select {
	case <-published:
		t.Fatal("offers published with invalid translations")
	default:
	}
	if err = os.Remove(filepath.Join(root, "locales", "invalid.cue")); err != nil {
		t.Fatal(err)
	}
	valid := receive(t, published)
	if offer, _ := valid.Offer("bread", items.Stackable); offer.BuyPrice != "12" {
		t.Fatal("repaired bundle missing offer update")
	}
	oldItem, err := valid.Capture("bread")
	if err != nil {
		t.Fatal(err)
	}

	if err = os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for {
		without := receive(t, published)
		if len(without.Offers()) == 0 {
			break
		}
	}

	// Recreate and then edit to demonstrate a fresh directory subscription.
	staged = shopAssets(t, staging)
	writeAsset(t, filepath.Join(staged, "bread.cue"), "package shop\nbread: {mode: \"stackable\", buyPrice: \"15\"}\n")
	if err = os.Rename(staged, dir); err != nil {
		t.Fatal(err)
	}
	for {
		recreated := receive(t, published)
		if offer, ok := recreated.Offer("bread", items.Stackable); ok && offer.BuyPrice == "15" {
			break
		}
	}
	writeAsset(t, filepath.Join(dir, "bread.cue"), "package shop\nbread: {mode: \"stackable\", buyPrice: \"16\"}\n")
	for {
		edited := receive(t, published)
		if offer, ok := edited.Offer("bread", items.Stackable); ok && offer.BuyPrice == "16" {
			newItem, err := edited.Capture("bread")
			if err != nil || newItem != oldItem {
				t.Fatalf("offer edit changed frozen item: %v", err)
			}
			break
		}
	}
}

func TestShopEmptyAndSeparateSnapshotCompatibility(t *testing.T) {
	root := copyAssets(t)
	missing, err := Load(root)
	if err != nil || len(missing.Offers()) != 0 {
		t.Fatalf("missing shop: %v", err)
	}

	snapshot, err := missing.Capture("bread")
	if err != nil {
		t.Fatal(err)
	}
	before, err := snapshot.Canonical()
	if err != nil {
		t.Fatal(err)
	}

	dir := shopAssets(t, root)
	empty, err := Load(root)
	if err != nil || len(empty.Offers()) != 0 {
		t.Fatalf("schema-only shop: %v", err)
	}

	writeAsset(t, filepath.Join(dir, "bread.cue"), "package shop\nbread: {mode: \"stackable\", buyPrice: \"10\", sellPrice: \"5\"}\n")
	active, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if offer, ok := active.Offer("bread", items.Stackable); !ok || offer.BuyPrice != "10" || offer.SellPrice != "5" {
		t.Fatalf("decoded offer: %+v %v", offer, ok)
	}

	saved, err := active.Capture("bread")
	if err != nil {
		t.Fatal(err)
	}
	after, err := saved.Canonical()
	if err != nil || string(before) != string(after) {
		t.Fatalf("shop changed snapshot JSON: %s %s %v", before, after, err)
	}

	writeAsset(t, filepath.Join(dir, "bread.cue"), "package shop\nbread: {mode: \"stackable\", sellPrice: \"5\"}\n")
	if err = os.Remove(filepath.Join(root, "items", "bread.cue")); err != nil {
		t.Fatal(err)
	}
	retired, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := retired.Offer(snapshot.CatalogID, snapshot.Mode); !ok {
		t.Fatal("old snapshot no longer sellable")
	}
}

func writeAsset(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidShopCandidates(t *testing.T) {
	for _, text := range []string{
		`bread: {mode: "individual", buyPrice: "10"}`,
		`missing: {mode: "stackable", buyPrice: "10"}`,
		`bread: {mode: "stackable"}`,
		`bread: {mode: "stackable", buyPrice: "10", sellPrice: "10"}`,
		`bread: {mode: "stackable", buyPrice: "0"}`,
		`bread: {mode: "stackable", buyPrice: "01"}`,
	} {
		t.Run(text, func(t *testing.T) {
			root := copyAssets(t)
			dir := shopAssets(t, root)
			writeAsset(t, filepath.Join(dir, "bread.cue"), "package shop\n"+text+"\n")
			if _, err := Load(root); err == nil {
				t.Fatal("invalid offer accepted")
			}
		})
	}
}
