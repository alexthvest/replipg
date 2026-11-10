package crafting

import (
	"math"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
)

func material() items.Snapshot {
	return items.Snapshot{
		Version:   1,
		CatalogID: "stick",
		Mode:      items.Stackable,
		Rarity:    "common",
		English: items.Text{
			Name:        "Stick",
			Description: "Wood",
		},
		Russian: items.Text{
			Name:        "Палка",
			Description: "Дерево",
		},
		Components: items.Components{
			Material: true,
			Weapon:   true,
		},
	}
}

func TestIdentityAggregatesAbsoluteSnapshots(t *testing.T) {
	s := material()
	a, err := Identify([]Allocation{{
		Entry:    "a",
		Snapshot: s,
		Quantity: 3,
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Identify([]Allocation{
		{
			Entry:    "b",
			Snapshot: s,
			Quantity: 1,
		},
		{
			Entry:    "c",
			Snapshot: s,
			Quantity: 2,
		},
	})
	if err != nil || !a.Equal(b) {
		t.Fatalf("split identity: %v", err)
	}
	c, _ := Identify([]Allocation{{
		Snapshot: s,
		Quantity: 6,
	}})
	if a.Equal(c) {
		t.Fatal("normalized quantities")
	}
	s.English.Description = "changed"
	c, _ = Identify([]Allocation{{
		Snapshot: s,
		Quantity: 3,
	}})
	if a.Equal(c) {
		t.Fatal("ignored snapshot text")
	}
	c.Digest = a.Digest
	if a.Equal(c) {
		t.Fatal("digest collision equality")
	}
	s = material()
	s.Mode = items.Individual
	d, _ := Identify([]Allocation{
		{
			Entry:    "instance1",
			Snapshot: s,
			Quantity: 1,
		},
		{
			Entry:    "instance2",
			Snapshot: s,
			Quantity: 1,
		},
	})
	e, _ := Identify([]Allocation{{
		Entry:    "different",
		Snapshot: s,
		Quantity: 2,
	}})
	if !d.Equal(e) {
		t.Fatal("instance identities leaked")
	}
}

func TestInvalidInputsAndAllocations(t *testing.T) {
	s := material()
	for _, allocations := range [][]Allocation{
		nil,
		{{
			Snapshot: s,
			Quantity: 0,
		}},
		{{
			Snapshot: s,
			Quantity: -1,
		}},
		{
			{
				Snapshot: s,
				Quantity: math.MaxInt64,
			},
			{
				Snapshot: s,
				Quantity: 1,
			},
		},
	} {
		if _, err := Identify(allocations); err == nil {
			t.Fatalf("accepted %+v", allocations)
		}
	}
	s.Components.Material = false
	if _, err := Identify([]Allocation{{
		Snapshot: s,
		Quantity: 1,
	}}); err == nil {
		t.Fatal("accepted nonmaterial")
	}
	s = material()
	entry := inventory.Entry{
		ID:        "one",
		Group:     -1,
		Character: "character",
		Snapshot:  s,
		Quantity:  10,
		Reserved:  3,
	}
	for _, test := range []struct {
		group       core.GroupID
		allocations []Allocation
	}{

		{
			group: -2,
			allocations: []Allocation{{
				Entry:    "one",
				Snapshot: s,
				Quantity: 1,
			}},
		},

		{
			group: -1,
			allocations: []Allocation{{
				Entry:    "one",
				Snapshot: s,
				Quantity: 8,
			}},
		},

		{
			group: -1,
			allocations: []Allocation{
				{
					Entry:    "one",
					Snapshot: s,
					Quantity: 1,
				},
				{
					Entry:    "one",
					Snapshot: s,
					Quantity: 1,
				},
			},
		},
	} {
		if err := ValidateAllocations(test.group, "character", test.allocations, []inventory.Entry{entry}); err == nil {
			t.Fatal("accepted unavailable allocation")
		}
	}
	if err := ValidateAllocations(-1, "character", []Allocation{{
		Entry:    "one",
		Snapshot: s,
		Quantity: 7,
	}}, []inventory.Entry{entry}); err != nil {
		t.Fatal(err)
	}
}

func TestOutcomeVariantsAndGeneratedDefinitions(t *testing.T) {
	s := material()
	for _, components := range []items.Components{
		{},
		{Material: true},
		{
			Weapon:     true,
			Consumable: true,
			Material:   true,
		},
	} {
		s.Components = components
		if err := (Outcome{
			Kind: Success,
			Item: &s,
		}).Validate(); err != nil {
			t.Fatal(err)
		}
	}
	if err := (Outcome{Kind: Uncraftable}).Validate(); err == nil {
		t.Fatal("accepted gameplay rejection")
	}
	for _, o := range []Outcome{
		{},
		{Kind: Success},
		{
			Kind: Uncraftable,
			Item: &s,
		},
		{Kind: "refusal"},
	} {
		if o.Validate() == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
	for _, mutate := range []func(*items.Snapshot){
		func(s *items.Snapshot) {
			s.Russian.Name = ""
		},
		func(s *items.Snapshot) {
			s.Mode = "invalid"
		},
		func(s *items.Snapshot) {
			s.Rarity = "invalid"
		},
		func(s *items.Snapshot) {
			s.English.Name = string(make([]byte, 129))
		},
	} {
		s = material()
		mutate(&s)
		if (Outcome{
			Kind: Success,
			Item: &s,
		}).Validate() == nil {
			t.Fatal("accepted invalid output")
		}
	}
}

func TestRecipeSelectionOrderAndEverySavedFieldMatter(t *testing.T) {
	first := material()
	second := first
	second.CatalogID = "coin"
	a, err := Identify([]Allocation{
		{
			Snapshot: first,
			Quantity: 2,
		},
		{
			Snapshot: second,
			Quantity: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Identify([]Allocation{
		{
			Snapshot: second,
			Quantity: 1,
		},
		{
			Snapshot: first,
			Quantity: 2,
		},
	})
	if err != nil || !a.Equal(b) {
		t.Fatal("selection order changed identity")
	}
	for _, mutate := range []func(*items.Snapshot){
		func(s *items.Snapshot) {
			s.Rarity = "rare"
		},
		func(s *items.Snapshot) {
			s.Mode = items.Individual
		},
		func(s *items.Snapshot) {
			s.Components.Consumable = true
		},
		func(s *items.Snapshot) {
			s.Russian.Description = "Другая версия"
		},
	} {
		changed := first
		mutate(&changed)
		c, err := Identify([]Allocation{
			{
				Snapshot: changed,
				Quantity: 2,
			},
			{
				Snapshot: second,
				Quantity: 1,
			},
		})
		if err != nil || a.Equal(c) {
			t.Fatal("saved field ignored")
		}
	}
}
