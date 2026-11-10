// Package crafting owns immutable recipe identity and crafting decisions.
package crafting

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
)

type Ingredient struct {
	Snapshot items.Snapshot `json:"snapshot"`
	Quantity int64          `json:"quantity"`
}

type Allocation struct {
	Entry    core.ID
	Snapshot items.Snapshot
	Quantity int64
}

type Identity struct {
	Canonical   []byte
	Digest      string
	Ingredients []Ingredient
}

var ErrIngredients = errors.New("invalid crafting ingredients")
var ErrUnavailable = errors.New("crafting inventory unavailable")

func Identify(allocations []Allocation) (Identity, error) {
	if len(allocations) == 0 {
		return Identity{}, ErrIngredients
	}

	quantities := map[string]int64{}
	snapshots := map[string]items.Snapshot{}
	for _, allocation := range allocations {
		if allocation.Quantity <= 0 || !allocation.Snapshot.Components.Material || allocation.Snapshot.Validate() != nil {
			return Identity{}, ErrIngredients
		}

		data, err := allocation.Snapshot.Canonical()
		if err != nil {
			return Identity{}, err
		}
		key := string(data)
		if quantities[key] > math.MaxInt64-allocation.Quantity {
			return Identity{}, ErrIngredients
		}
		quantities[key] += allocation.Quantity
		snapshots[key] = allocation.Snapshot
	}

	keys := make([]string, 0, len(quantities))
	for key := range quantities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ingredients := make([]Ingredient, 0, len(keys))
	for _, key := range keys {
		ingredients = append(ingredients, Ingredient{
			Snapshot: snapshots[key],
			Quantity: quantities[key],
		})
	}

	data, err := json.Marshal(struct {
		Version     int          `json:"version"`
		Ingredients []Ingredient `json:"ingredients"`
	}{
		Version:     1,
		Ingredients: ingredients,
	})
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		Canonical:   data,
		Digest:      items.Digest(data),
		Ingredients: ingredients,
	}, nil
}

func (i Identity) Equal(other Identity) bool {
	return bytes.Equal(i.Canonical, other.Canonical)
}

func ValidateAllocations(group core.GroupID, character core.ID, allocations []Allocation, entries []inventory.Entry) error {
	if _, err := Identify(allocations); err != nil {
		return err
	}

	seen := map[core.ID]bool{}
	for _, a := range allocations {
		if a.Entry == "" || seen[a.Entry] {
			return ErrIngredients
		}

		seen[a.Entry] = true
		found := false
		for _, entry := range entries {
			if entry.ID != a.Entry {
				continue
			}
			if entry.Group != group || entry.Character != character || entry.Snapshot != a.Snapshot || entry.Available() < a.Quantity || (entry.Snapshot.Mode == items.Individual && a.Quantity != 1) {
				return ErrUnavailable
			}
			found = true
		}
		if !found {
			return ErrUnavailable
		}
	}
	return nil
}
