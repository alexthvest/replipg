package character

import (
	"errors"
	"github.com/alexthvest/replipg/internal/character/contract"
	"strings"
	"testing"
	"time"
)

func TestRenderSummary(t *testing.T) {
	snapshot := contract.Snapshot{Health: 100, MaxHealth: 100, Attack: 10, Defense: 5, State: contract.StateActive}
	first := renderSummary(snapshot, true)
	for _, part := range []string{"Character created", "100/100", "Attack: 10", "Defense: 5", "active"} {
		if !strings.Contains(first, part) {
			t.Fatalf("missing %s", part)
		}
	}
	if strings.Contains(renderSummary(snapshot, false), "created") {
		t.Fatal("repeat creation notice")
	}
	snapshot.Health = 0
	snapshot.State = contract.StateIncapacitated
	snapshot.RecoverAt = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	got := renderSummary(snapshot, false)
	if !strings.Contains(got, "incapacitated") || !strings.Contains(got, "2026-10-27T12:00:00Z") {
		t.Fatal(got)
	}
}
func TestRenderDomainError(t *testing.T) {
	if got := renderDomainError(contract.ErrIncapacitated); got == "" {
		t.Fatal("missing rejection")
	}
	if got := renderDomainError(errors.New("private fixture")); got != "" {
		t.Fatal("unexpected error rendered")
	}
}
