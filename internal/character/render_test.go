package character

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/localization"
)

func TestRenderSummary(t *testing.T) {
	snapshot := contract.Snapshot{Health: 100, MaxHealth: 100, Attack: 10, Defense: 5, State: contract.StateActive}
	catalog := englishPresentation(t).Catalog
	first, err := renderLocalizedSummary(catalog, localization.English, snapshot, true)
	if err != nil {
		t.Fatal(err)
	}

	for _, part := range []string{"Character created", "100/100", "Attack: 10", "Defense: 5", "active"} {
		if !strings.Contains(first.Text, part) {
			t.Fatalf("missing %s", part)
		}
	}

	again, err := renderLocalizedSummary(catalog, localization.English, snapshot, false)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(again.Text, "created") {
		t.Fatal("repeat creation notice")
	}

	snapshot.Health = 0
	snapshot.State = contract.StateIncapacitated
	snapshot.RecoverAt = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	got, err := renderLocalizedSummary(catalog, localization.English, snapshot, false)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got.Text, "incapacitated") || !strings.Contains(got.Text, "2026-10-27T12:00:00Z") {
		t.Fatal(got)
	}
}

func TestRenderDomainError(t *testing.T) {
	catalog := englishPresentation(t).Catalog
	if got, err := renderLocalizedDomainError(catalog, localization.English, contract.ErrIncapacitated); err != nil || got == "" {
		t.Fatal("missing rejection")
	}

	if got, err := renderLocalizedDomainError(catalog, localization.English, errors.New("private fixture")); err != nil || got != "" {
		t.Fatal("unexpected error rendered")
	}
}
