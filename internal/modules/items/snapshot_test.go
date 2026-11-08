package items

import (
	"bytes"
	"testing"
)

func TestSnapshotEqualityUsesAllSavedData(t *testing.T) {
	a := Snapshot{
		Version:   1,
		CatalogID: "coin",
		Mode:      Stackable,
		Rarity:    "common",
		English: Text{
			Name:        "Coin",
			Description: "English",
		},
		Russian: Text{
			Name:        "Монета",
			Description: "Русский",
		},
	}
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}

	first, err := a.Canonical()
	if err != nil {
		t.Fatal(err)
	}

	second, err := a.Canonical()
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Fatal("noncanonical snapshot")
	}

	for _, mutate := range []func(*Snapshot){
		func(s *Snapshot) {
			s.English.Name = "Edited"
		},
		func(s *Snapshot) {
			s.Russian.Description = "Изменено"
		},
		func(s *Snapshot) {
			s.Rarity = "epic"
		},
		func(s *Snapshot) {
			s.Mode = Individual
		},
		func(s *Snapshot) {
			s.CatalogID = "other"
		},
	} {
		b := a
		mutate(&b)
		if a.Equal(b) {
			t.Fatal("differing snapshots compare equal")
		}
	}

	b := a
	b.English.Name = "changed"
	if a.English.Name != "Coin" {
		t.Fatal("snapshot aliased")
	}
}
