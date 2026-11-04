package item_test

import (
	"errors"
	"testing"

	"github.com/alexthvest/replipg/internal/item"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
)

func TestImmutableSeed(t *testing.T) {
	c, err := item.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}

	items := c.Items()
	items[0].ID = "changed"

	if _, ok := c.Lookup("coin"); !ok {
		t.Fatal("mutable catalog")
	}

	e, err := c.Effect("healing_potion")
	if err != nil || e.HealingHP != 10 {
		t.Fatalf("effect: %+v %v", e, err)
	}

	if _, err := c.Effect("coin"); !errors.Is(err, itemcontract.ErrNotUsable) {
		t.Fatal(err)
	}
}
