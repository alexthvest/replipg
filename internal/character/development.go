package character

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type healthService interface {
	inspector
	ApplyHealth(context.Context, runtimecontract.DBTX, contract.HealthChange) (contract.MutationResult, error)
}

// RegisterDevelopment installs self-only health probes. Composition must opt in
// explicitly; production registration never invokes this function.
func RegisterDevelopment(registry *interaction.Registry, service healthService, now func() time.Time, presentation Presentation) error {
	if err := presentation.validate(); err != nil {
		return err
	}

	if registry == nil || service == nil || reflect.ValueOf(service).Kind() == reflect.Pointer && reflect.ValueOf(service).IsNil() || now == nil {
		return contract.ErrInvalidInput
	}

	handler := developmentHandler{service: service, now: now, presentation: presentation}
	if err := registry.RegisterCommand("dev_character_damage", handler); err != nil {
		return err
	}

	return registry.RegisterCommand("dev_character_defeat", handler)
}

type developmentHandler struct {
	presentation Presentation
	service      healthService
	now          func() time.Time
}

func (h developmentHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	command, ok := personalCommand(event, h.now())
	if !ok {
		return interaction.HandlerResult{}, nil
	}

	locale, err := h.presentation.Resolve(ctx, scope.DBTX(), command.Chat)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	translate := func(key localization.Key) (string, error) { return h.presentation.Catalog.Text(locale, key, nil) }
	args := strings.Fields(command.Arguments)
	var amount int64

	switch command.Name {
	case "dev_character_damage":
		if len(args) != 1 {
			text, err := translate("usage.damage")
			if err != nil {
				return interaction.HandlerResult{}, err
			}

			return personalResponse(command, text), nil
		}

		value, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || value < 0 {
			text, err := translate("usage.damage")
			if err != nil {
				return interaction.HandlerResult{}, err
			}

			return personalResponse(command, text), nil
		}

		amount = value
	case "dev_character_defeat":
		if len(args) != 0 {
			text, err := translate("usage.defeat")
			if err != nil {
				return interaction.HandlerResult{}, err
			}

			return personalResponse(command, text), nil
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
		text, renderErr := renderLocalizedDomainError(h.presentation.Catalog, locale, err)
		if renderErr != nil {
			return interaction.HandlerResult{}, renderErr
		}

		if text == "" {
			return interaction.HandlerResult{}, err
		}

		summary, renderErr := renderLocalizedSummary(h.presentation.Catalog, locale, snapshot, created)
		if renderErr != nil {
			return interaction.HandlerResult{}, renderErr
		}

		return personalResponse(command, text+"\n"+summary), nil
	}

	text, err := renderLocalizedSummary(h.presentation.Catalog, locale, mutation.Current, created)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	result := personalResponse(command, text)
	if err := runtimecontract.ValidateResponse(result.ImmediateResponses[0]); err != nil {
		return interaction.HandlerResult{}, err
	}

	return result, nil
}
