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
	if entry.Group >= 0 || entry.ID == "" || entry.Character == "" || entry.Quantity <= 0 || entry.Reserved < 0 || entry.Reserved > entry.Quantity {
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

func (p *Projection) Remove(world donburi.World, group core.GroupID, id core.ID) {
	k := key{
		group: group,
		id:    id,
	}
	if entity, exists := p.entities[k]; exists {
		world.Remove(entity)
		delete(p.entities, k)
	}
}

// Reconcile replaces one character's projection after a committed mutation.
func (p *Projection) Reconcile(
	world donburi.World,
	group core.GroupID,
	character core.ID,
	entries []Entry,
) error {
	retained := make(map[core.ID]bool, len(entries))
	for _, entry := range entries {
		if entry.Group != group || entry.Character != character {
			return errors.New("reconcile inventory: foreign entry")
		}
		if err := p.Apply(world, entry); err != nil {
			return err
		}
		retained[entry.ID] = true
	}

	for k, entity := range p.entities {
		entry := component.Get(world.Entry(entity))
		if k.group == group && entry.Character == character && !retained[k.id] {
			p.Remove(world, group, k.id)
		}
	}
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
