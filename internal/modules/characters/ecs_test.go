package characters

import (
	"testing"

	"github.com/yohamta/donburi"
)

func TestProjectionUsesDurableAndGroupIdentity(t *testing.T) {
	world := donburi.NewWorld()
	p := NewProjection()
	c := Character{
		ID:    "durable",
		Group: -1,
		User:  1,
		Name:  "Name",
	}
	if err := p.Apply(world, c); err != nil {
		t.Fatal(err)
	}

	if _, ok := p.Get(world, -2, c.ID); ok {
		t.Fatal("foreign group found")
	}

	c.Name = "renamed"
	if err := p.Apply(world, c); err != nil {
		t.Fatal(err)
	}

	saved, ok := p.Get(world, -1, c.ID)
	if !ok || saved.Name != "renamed" || world.Len() != 1 {
		t.Fatal(saved)
	}
}
