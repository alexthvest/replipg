package crafting

import (
	"errors"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/items"
)

const (
	Success = "success"
	// Uncraftable is retained only to read historical attempt results.
	Uncraftable      = "uncraftable"
	Pending          = "pending"
	Unavailable      = "unavailable"
	TechnicalFailure = "technical-failure"
)

type Outcome struct {
	Kind string          `json:"kind"`
	Item *items.Snapshot `json:"item,omitempty"`
}

func (o Outcome) Validate() error {
	if o.Kind != Success || o.Item == nil {
		return errors.New("invalid crafting outcome variant")
	}
	return o.Item.Validate()
}

type Attempt struct {
	ID          core.ID
	Token       core.ID
	Scope       core.Scope
	Character   core.ID
	MessageID   int
	Recipe      core.ID
	Allocations []Allocation
	State       string
	Outcome     Outcome
}
