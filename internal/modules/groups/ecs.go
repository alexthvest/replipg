package groups

import (
	"errors"
	"slices"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/yohamta/donburi"
)

// Projection is accessed exclusively by the runtime owner.
type Projection struct {
	entities map[core.GroupID]donburi.Entity
}

var component = donburi.NewComponentType[State]()

func NewProjection() *Projection {
	return &Projection{entities: make(map[core.GroupID]donburi.Entity)}
}

func (p *Projection) Apply(world donburi.World, state State) error {
	if state.ID >= 0 {
		return errors.New("project group: invalid identity")
	}

	entity, ok := p.entities[state.ID]
	if !ok {
		entity = world.Create(component)
		p.entities[state.ID] = entity
	}

	state.Admins = slices.Clone(state.Admins)
	component.SetValue(world.Entry(entity), state)
	return nil
}

func (p *Projection) Get(world donburi.World, id core.GroupID) (State, bool) {
	entity, ok := p.entities[id]
	if !ok {
		return State{}, false
	}

	state := *component.Get(world.Entry(entity))
	state.Admins = slices.Clone(state.Admins)
	return state, true
}
