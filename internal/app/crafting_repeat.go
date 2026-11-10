package app

import (
	"strconv"

	"github.com/alexthvest/replipg/internal/core"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/ui/menus"
)

// restoreCraft prepares a new composition, never a durable attempt. Shortage
// rows retain their requested snapshot and quantity without claiming ownership.
func restoreCraft(selection *menus.CraftSelection, allocations []domain.Allocation, entries []inventory.Entry) map[core.ID]items.Snapshot {
	var picks []menus.CraftPick
	snapshots := map[core.ID]items.Snapshot{}
	used := map[core.ID]int64{}
	positions := map[core.ID]int{}
	for i, allocation := range allocations {
		remaining := allocation.Quantity
		allocate := func(entry inventory.Entry) {
			if entry.Snapshot != allocation.Snapshot || remaining <= 0 {
				return
			}
			quantity := min(remaining, entry.Available()-used[entry.ID])
			if quantity <= 0 {
				return
			}
			if position, found := positions[entry.ID]; found {
				picks[position].Quantity += quantity
			} else {
				positions[entry.ID] = len(picks)
				picks = append(picks, menus.CraftPick{
					Entry:    entry.ID,
					Quantity: quantity,
				})
			}
			used[entry.ID] += quantity
			snapshots[entry.ID] = entry.Snapshot
			remaining -= quantity
		}
		for _, entry := range entries {
			if entry.ID == allocation.Entry {
				allocate(entry)
				break
			}
		}
		for _, entry := range entries {
			if entry.ID != allocation.Entry {
				allocate(entry)
			}
		}
		if remaining > 0 {
			// Inventory IDs are generated UUIDs; this namespace is display-only.
			id := core.ID("craft-shortage:" + strconv.Itoa(i))
			picks = append(picks, menus.CraftPick{
				Entry:    id,
				Quantity: remaining,
			})
			snapshots[id] = allocation.Snapshot
		}
	}
	selection.Restore(picks)
	return snapshots
}

// availableCraftOutput excludes reservations and different saved item versions.
func availableCraftOutput(entries []inventory.Entry, snapshot items.Snapshot) (inventory.Entry, bool) {
	for _, entry := range entries {
		if entry.Snapshot == snapshot && entry.Available() > 0 {
			return entry, true
		}
	}
	return inventory.Entry{}, false
}
