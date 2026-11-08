package inventory

import (
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/yohamta/donburi"
)

func TestProjectionReconcilesRemovalAndReservation(t *testing.T) {
	world := donburi.NewWorld()
	projection := NewProjection()
	first := Entry{
		ID:        "first",
		Group:     -1,
		Character: "owner",
		Quantity:  10,
		Reserved:  3,
	}
	other := Entry{
		ID:        "other",
		Group:     -1,
		Character: "another",
		Quantity:  1,
	}
	for _, entry := range []Entry{first, other} {
		if err := projection.Apply(world, entry); err != nil {
			t.Fatal(err)
		}
	}

	saved, found := projection.Get(world, -1, first.ID)
	if !found || saved.Quantity != 10 || saved.Available() != 7 {
		t.Fatalf("reservation projection: %+v %v", saved, found)
	}
	first.Quantity = 7
	first.Reserved = 0
	if err := projection.Reconcile(world, -1, "owner", []Entry{first}); err != nil {
		t.Fatal(err)
	}
	saved, found = projection.Get(world, -1, first.ID)
	if !found || saved.Quantity != 7 || saved.Reserved != 0 {
		t.Fatalf("partial sale projection: %+v %v", saved, found)
	}
	if err := projection.Reconcile(world, -1, "owner", nil); err != nil {
		t.Fatal(err)
	}

	if _, found = projection.Get(world, -1, first.ID); found {
		t.Fatal("deleted inventory retained in runtime projection")
	}

	if _, found = projection.Get(world, -1, other.ID); !found {
		t.Fatal("reconciliation removed another character's item")
	}
	projection.Remove(world, -1, core.ID("absent"))
	if err := projection.Reconcile(world, -2, "owner", []Entry{other}); err == nil {
		t.Fatal("foreign entry accepted")
	}
	first.Reserved = 8
	if err := projection.Apply(world, first); err == nil {
		t.Fatal("over-reserved projection accepted")
	}
}
