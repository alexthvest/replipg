package contract_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestContractValues(t *testing.T) {
	character := contract.Snapshot{ID: contract.ID("opaque-character"), UserID: runtimecontract.UserID(42), MaxHealth: 100, Health: 70, Attack: 10, Defense: 5, State: contract.StateActive, Revision: 2}
	change := contract.HealthChange{OperationID: "battle:1:target:1", CharacterID: character.ID, Kind: contract.HealthDamage, Amount: 7}
	if err := change.Validate(); err != nil {
		t.Fatal(err)
	}
	result := contract.MutationResult{Applied: false, AppliedRevision: 1, Current: character}
	if result.Current != character || result.AppliedRevision != 1 {
		t.Fatal("replay must carry current snapshot and original revision")
	}
	eligibility := contract.Eligibility{Kind: contract.EligibilityMissing}
	if eligibility.Character != (contract.Snapshot{}) {
		t.Fatal("missing eligibility must permit zero snapshot")
	}
	character.State, character.RecoverAt = contract.StateIncapacitated, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !character.State.Valid() {
		t.Fatal("incapacitated state unsupported")
	}
	for _, kind := range []contract.EligibilityKind{contract.EligibilityMissing, contract.EligibilityActive, contract.EligibilityIncapacitated} {
		if !kind.Valid() {
			t.Fatalf("unsupported eligibility %q", kind)
		}
	}
	if contract.State("").Valid() || contract.HealthKind("set").Valid() || contract.EligibilityKind("other").Valid() {
		t.Fatal("unknown enum accepted")
	}
}

func TestHealthChangeValidation(t *testing.T) {
	valid := contract.HealthChange{OperationID: contract.OperationID(strings.Repeat("x", 128)), CharacterID: "opaque", Kind: contract.HealthDamage, Amount: 0}
	tests := []struct {
		name   string
		change contract.HealthChange
		valid  bool
	}{
		{"zero damage", valid, true},
		{"maximum whole HP", contract.HealthChange{OperationID: "op", CharacterID: "id", Kind: contract.HealthHeal, Amount: 1<<63 - 1}, true},
		{"empty operation", contract.HealthChange{CharacterID: "id", Kind: contract.HealthDamage}, false},
		{"long operation", contract.HealthChange{OperationID: contract.OperationID(strings.Repeat("x", 129)), CharacterID: "id", Kind: contract.HealthDamage}, false},
		{"byte length", contract.HealthChange{OperationID: contract.OperationID(strings.Repeat("é", 65)), CharacterID: "id", Kind: contract.HealthDamage}, false},
		{"empty character", contract.HealthChange{OperationID: "op", Kind: contract.HealthDamage}, false},
		{"unknown kind", contract.HealthChange{OperationID: "op", CharacterID: "id", Kind: "set"}, false},
		{"negative amount", contract.HealthChange{OperationID: "op", CharacterID: "id", Kind: contract.HealthHeal, Amount: -1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.change.Validate()
			if tt.valid {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var domain *contract.DomainError
			if !errors.Is(err, contract.ErrInvalidInput) || !errors.As(err, &domain) {
				t.Fatalf("expected inspectable invalid input, got %v", err)
			}
		})
	}
}
