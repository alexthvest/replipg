package menus

import (
	"slices"

	"github.com/alexthvest/replipg/internal/core"
)

type CraftPick struct {
	Entry    core.ID
	Quantity int64
}

// CraftSelection retains edits across material pages. Only the runtime owner uses it.
// Snapshot validation remains in the application, where inventory is available.
type CraftSelection struct {
	Page         int
	MaterialPage int
	ResultPage   int
	Draft        *CraftDraft
	picks        []CraftPick
	confirmation core.ID
	frozen       []CraftPick
}

// CraftDraft keeps pending edits separate from the committed composition.
type CraftDraft struct {
	Entry    core.ID
	Quantity int64
	Origin   string
}

func (s *CraftSelection) Edit(id core.ID, origin string) {
	s.Draft = &CraftDraft{
		Entry:    id,
		Quantity: max(1, s.Quantity(id)),
		Origin:   origin,
	}
}

func (s *CraftSelection) Save(available int64, individual bool) error {
	if s.Draft == nil {
		return ErrInvalid
	}
	if err := s.Set(s.Draft.Entry, s.Draft.Quantity, available, individual); err != nil {
		return err
	}
	s.Draft = nil
	return nil
}

func (s *CraftSelection) Clear() {
	*s = CraftSelection{}
}

// Restore retains requested quantities, including unresolved shortage rows.
func (s *CraftSelection) Restore(picks []CraftPick) {
	s.Clear()
	s.picks = slices.Clone(picks)
}

func (s *CraftSelection) Quantity(id core.ID) int64 {
	for _, pick := range s.picks {
		if pick.Entry == id {
			return pick.Quantity
		}
	}
	return 0
}

func (s *CraftSelection) Set(id core.ID, quantity, available int64, individual bool) error {
	if id == "" || available <= 0 || quantity <= 0 || quantity > available || (individual && quantity != 1) {
		return ErrInvalid
	}
	for i, pick := range s.picks {
		if pick.Entry == id {
			if pick.Quantity != quantity {
				s.picks[i].Quantity = quantity
				s.invalidate()
			}
			return nil
		}
	}
	s.picks = append(s.picks, CraftPick{
		Entry:    id,
		Quantity: quantity,
	})
	s.invalidate()
	return nil
}

func (s *CraftSelection) Remove(id core.ID) {
	for i, pick := range s.picks {
		if pick.Entry == id {
			s.picks = slices.Delete(s.picks, i, i+1)
			s.invalidate()
			return
		}
	}
}

func (s *CraftSelection) Picks() []CraftPick {
	return slices.Clone(s.picks)
}

func (s *CraftSelection) Quantities() map[core.ID]int64 {
	result := make(map[core.ID]int64, len(s.picks))
	for _, pick := range s.picks {
		result[pick.Entry] = pick.Quantity
	}
	return result
}

func (s *CraftSelection) Freeze(token core.ID) []CraftPick {
	if token == "" || len(s.picks) == 0 {
		return nil
	}
	s.confirmation = token
	s.frozen = slices.Clone(s.picks)
	return slices.Clone(s.frozen)
}

func (s *CraftSelection) Confirmation() core.ID {
	return s.confirmation
}

func (s *CraftSelection) Frozen(token core.ID) ([]CraftPick, error) {
	if token == "" || token != s.confirmation || !slices.Equal(s.frozen, s.picks) {
		return nil, ErrInvalid
	}
	return slices.Clone(s.frozen), nil
}

func (s *CraftSelection) invalidate() {
	s.confirmation = ""
	s.frozen = nil
}

type CraftQuantityChoice struct {
	Caption  string
	Quantity int64
}

// CraftQuantityChoices clamps before arithmetic, including at MaxInt64.
func CraftQuantityChoices(current, available int64, maxCaption string) []CraftQuantityChoice {
	if available <= 0 {
		return nil
	}
	current = min(available, max(1, current))
	return []CraftQuantityChoice{
		{
			Caption:  "1",
			Quantity: 1,
		},
		{
			Caption:  "-10",
			Quantity: max(1, current-10),
		},
		{
			Caption:  "-1",
			Quantity: max(1, current-1),
		},
		{
			Caption:  "+1",
			Quantity: current + min(1, available-current),
		},
		{
			Caption:  "+10",
			Quantity: current + min(10, available-current),
		},
		{
			Caption:  maxCaption,
			Quantity: available,
		},
	}
}
