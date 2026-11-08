package items

import (
	"bytes"
	"encoding/json"
	"strings"
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

// Literal bytes and digest from the version-1 format before components existed.
const legacyJSON = `{"version":1,"catalog_id":"coin","mode":"stackable","rarity":"common","en":{"name":"Coin","description":"English"},"ru":{"name":"Монета","description":"Русский"}}`

func TestSnapshotComponentCompatibility(t *testing.T) {
	var legacy Snapshot
	if err := json.Unmarshal([]byte(legacyJSON), &legacy); err != nil {
		t.Fatal(err)
	}

	for _, input := range []string{legacyJSON, strings.TrimSuffix(legacyJSON, "}") + `,"components":{}}`} {
		var saved Snapshot
		if err := json.Unmarshal([]byte(input), &saved); err != nil {
			t.Fatal(err)
		}

		data, err := saved.Canonical()
		if err != nil || string(data) != legacyJSON || !saved.Equal(legacy) || Digest(data) != "0b20e996759455025e77f067737bdc07d7cbd6ea6f6fa324cf62b4b8659d69db" {
			t.Fatalf("legacy format drift: %s %v", data, err)
		}
	}

	var previous Snapshot
	for _, object := range []string{`{"weapon":{},"material":{},"consumable":{}}`, `{"consumable":{},"material":{},"weapon":{}}`} {
		var saved Snapshot
		input := strings.TrimSuffix(legacyJSON, "}") + `,"components":` + object + `}`
		if err := json.Unmarshal([]byte(input), &saved); err != nil {
			t.Fatal(err)
		}

		if err := saved.Validate(); err != nil {
			t.Fatal(err)
		}

		data, err := saved.Canonical()
		if err != nil || !strings.HasSuffix(string(data), `"components":{"consumable":{},"material":{},"weapon":{}}}`) {
			t.Fatalf("canonical components: %s %v", data, err)
		}

		if previous.Version != 0 {
			before, _ := previous.Canonical()
			if !saved.Equal(previous) || !bytes.Equal(data, before) {
				t.Fatal("declaration order changed identity")
			}
		}

		var restored Snapshot
		if err := json.Unmarshal(data, &restored); err != nil || restored != saved {
			t.Fatalf("round trip: %+v %v", restored, err)
		}

		changed := saved
		changed.Components.Material = false
		other, _ := changed.Canonical()
		if saved.Equal(changed) || bytes.Equal(data, other) || saved.Equal(legacy) {
			t.Fatal("membership missing from identity")
		}

		previous = saved
	}

	for _, object := range []string{`null`, `[]`, `{"weapon":null}`, `{"unknown":{}}`} {
		var saved Snapshot
		if err := json.Unmarshal([]byte(strings.TrimSuffix(legacyJSON, "}")+`,"components":`+object+`}`), &saved); err == nil {
			t.Fatalf("invalid snapshot components: %s", object)
		}
	}
}
