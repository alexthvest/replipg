package inventory

import (
	"errors"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/yohamta/donburi"
)

var component = donburi.NewComponentType[Entry]()

type key struct {
	group core.GroupID
	id    core.ID
}

type Projection struct {
	entities map[key]donburi.Entity
}

func NewProjection() *Projection {
	return &Projection{entities: map[key]donburi.Entity{}}
}

func (p *Projection) Apply(world donburi.World, entry Entry) error {
	if entry.Group >= 0 || entry.ID == "" || entry.Character == "" || entry.Quantity <= 0 {
		return errors.New("project inventory: invalid identity or quantity")
	}

	k := key{
		entry.Group,
		entry.ID,
	}
	entity, ok := p.entities[k]
	if !ok {
		entity = world.Create(component)
		p.entities[k] = entity
	}

	component.SetValue(world.Entry(entity), entry)
	return nil
}

func (p *Projection) Get(world donburi.World, group core.GroupID, id core.ID) (Entry, bool) {
	entity, ok := p.entities[key{
		group,
		id,
	}]
	if !ok {
		return Entry{}, false
	}
	return *component.Get(world.Entry(entity)), true
}
