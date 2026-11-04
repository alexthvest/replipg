package character

import (
	"context"
	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type healthService interface {
	inspector
	ApplyHealth(context.Context, runtimecontract.DBTX, contract.HealthChange) (contract.MutationResult, error)
}

// RegisterDevelopment installs self-only health probes. Composition must opt in
// explicitly; production registration never invokes this function.
func RegisterDevelopment(registry *interaction.Registry, service healthService, now func() time.Time) error {
	if registry == nil || service == nil || reflect.ValueOf(service).Kind() == reflect.Pointer && reflect.ValueOf(service).IsNil() || now == nil {
		return &contract.DomainError{Kind: contract.ErrorInvalidInput}
	}
	handler := developmentHandler{service: service, now: now}
	if err := registry.RegisterCommand("dev_character_damage", handler); err != nil {
		return err
	}
	return registry.RegisterCommand("dev_character_defeat", handler)
}

type developmentHandler struct {
	service healthService
	now     func() time.Time
}

func (h developmentHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	command, ok := personalCommand(event, h.now())
	if !ok {
		return interaction.HandlerResult{}, nil
	}
	args := strings.Fields(command.Arguments)
	var amount int64
	switch command.Name {
	case "dev_character_damage":
		if len(args) != 1 {
			return personalResponse(command, "Usage: /dev_character_damage <whole HP>"), nil
		}
		value, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || value < 0 {
			return personalResponse(command, "Usage: /dev_character_damage <whole HP>"), nil
		}
		amount = value
	case "dev_character_defeat":
		if len(args) != 0 {
			return personalResponse(command, "Usage: /dev_character_defeat"), nil
		}
	default:
		return interaction.HandlerResult{}, nil
	}
	snapshot, created, err := h.service.Ensure(ctx, scope.DBTX(), command.User)
	if err != nil {
		return interaction.HandlerResult{}, err
	}
	if command.Name == "dev_character_defeat" {
		amount = snapshot.Health
	}
	operation := contract.OperationID("character:development:" + string(command.Name) + ":" + strconv.FormatInt(int64(command.Update), 10) + ":" + string(snapshot.ID))
	mutation, err := h.service.ApplyHealth(ctx, scope.DBTX(), contract.HealthChange{OperationID: operation, CharacterID: snapshot.ID, Kind: contract.HealthDamage, Amount: amount})
	if err != nil {
		text := renderDomainError(err)
		if text == "" {
			return interaction.HandlerResult{}, err
		}
		return personalResponse(command, text+"\n"+renderSummary(snapshot, created)), nil
	}
	result := personalResponse(command, renderSummary(mutation.Current, created))
	if err := runtimecontract.ValidateResponse(result.ImmediateResponses[0]); err != nil {
		return interaction.HandlerResult{}, err
	}
	return result, nil
}
