package character

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type fakeHealthService struct {
	fakeInspector
	changes     []contract.HealthChange
	mutationErr error
}

func (s *fakeHealthService) ApplyHealth(_ context.Context, _ runtimecontract.DBTX, change contract.HealthChange) (contract.MutationResult, error) {
	s.changes = append(s.changes, change)
	return contract.MutationResult{Applied: true, AppliedRevision: 2, Current: contract.Snapshot{ID: change.CharacterID, Health: 0, MaxHealth: 100, State: contract.StateIncapacitated, RecoverAt: commandNow.Add(time.Second)}}, s.mutationErr
}

func TestDevelopmentCommands(t *testing.T) {
	for _, tc := range []struct {
		name   interaction.CommandName
		args   string
		valid  bool
		amount int64
	}{
		{"dev_character_damage", "12", true, 12}, {"dev_character_defeat", "", true, 100},
		{"dev_character_damage", "-1", false, 0}, {"dev_character_damage", "1.5", false, 0}, {"dev_character_damage", "12 42", false, 0}, {"dev_character_damage", "", false, 0}, {"dev_character_defeat", "42", false, 0},
	} {
		t.Run(string(tc.name)+tc.args, func(t *testing.T) {
			service := &fakeHealthService{}
			h := developmentHandler{service: service, now: func() time.Time { return commandNow }, presentation: englishPresentation(t)}
			cmd := authorizedCommand()
			cmd.Name = tc.name
			cmd.Arguments = tc.args
			result, err := h.Handle(context.Background(), commandScope{}, cmd)
			if err != nil || len(result.ImmediateResponses) != 1 {
				t.Fatalf("result=%v err=%v", result, err)
			}

			if !tc.valid {
				if service.calls != 0 || len(service.changes) != 0 {
					t.Fatal("invalid command mutated")
				}

				return
			}

			if len(service.changes) != 1 || service.changes[0].Amount != tc.amount || service.changes[0].Kind != contract.HealthDamage || !strings.HasPrefix(string(service.changes[0].OperationID), "character:development:") {
				t.Fatalf("mutation %+v", service.changes)
			}

			response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

			if response.TargetUserID != 7 {
				t.Fatal("target spoofed")
			}

			cmd.ActorIsBot = true
			if _, err := h.Handle(context.Background(), commandScope{}, cmd); err != nil {
				t.Fatal(err)
			}

			if len(service.changes) != 1 {
				t.Fatal("bot action applied")
			}
		})
	}
}

func TestDevelopmentDomainRejectionIsRestricted(t *testing.T) {
	service := &fakeHealthService{mutationErr: contract.ErrIncapacitated}
	h := developmentHandler{service: service, now: func() time.Time { return commandNow }, presentation: englishPresentation(t)}
	cmd := authorizedCommand()
	cmd.Name = "dev_character_damage"
	cmd.Arguments = "5"
	result, err := h.Handle(context.Background(), commandScope{}, cmd)
	if err != nil || len(result.ImmediateResponses) != 1 {
		t.Fatal("missing rejection response")
	}

	response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

	if !strings.Contains(response.Content.Text, "incapacitated") {
		t.Fatal(response.Content.Text)
	}
}
