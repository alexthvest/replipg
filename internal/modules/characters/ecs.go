package characters

import (
	"errors"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/yohamta/donburi"
)

var component = donburi.NewComponentType[Character]()

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

func (p *Projection) Apply(world donburi.World, character Character) error {
	if character.Group >= 0 || character.ID == "" || character.User <= 0 {
		return errors.New("project character: invalid identity")
	}

	k := key{
		character.Group,
		character.ID,
	}
	entity, ok := p.entities[k]
	if !ok {
		entity = world.Create(component)
		p.entities[k] = entity
	}

	component.SetValue(world.Entry(entity), character)
	return nil
}

func (p *Projection) Get(world donburi.World, group core.GroupID, id core.ID) (Character, bool) {
	entity, ok := p.entities[key{
		group,
		id,
	}]
	if !ok {
		return Character{}, false
	}
	return *component.Get(world.Entry(entity)), true
}
