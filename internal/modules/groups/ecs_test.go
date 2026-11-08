package groups

import (
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/yohamta/donburi"
)

func TestScopedProjection(t *testing.T) {
	world := donburi.NewWorld()
	projection := NewProjection()

	for _, state := range []State{
		{
			ID:       -1,
			Language: "ru",
			Admins:   []core.UserID{2},
		},
		{
			ID:       -2,
			Language: "en",
		},
	} {
		if err := projection.Apply(world, state); err != nil {
			t.Fatal(err)
		}
	}

	a, ok := projection.Get(world, -1)
	if !ok || a.Language != "ru" {
		t.Fatal(a)
	}

	a.Admins[0] = 999
	b, ok := projection.Get(world, -2)
	if !ok || b.Language != "en" || len(b.Admins) != 0 {
		t.Fatal(b)
	}

	a, _ = projection.Get(world, -1)
	if a.Admins[0] != 2 {
		t.Fatal("mutable projection escaped")
	}

	if _, ok = projection.Get(world, -3); ok {
		t.Fatal("foreign group found")
	}
}
