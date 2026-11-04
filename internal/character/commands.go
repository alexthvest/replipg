package character

import (
	"context"
	"reflect"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	sharingcontract "github.com/alexthvest/replipg/internal/sharing/contract"
)

type inspector interface {
	Ensure(context.Context, runtimecontract.DBTX, runtimecontract.UserID) (contract.Snapshot, bool, error)
}

// Register installs the actor-only personal character command. The clock checks
// transport authorization and must be wall time in production.
func Register(registry *interaction.Registry, service inspector, now func() time.Time, presentation Presentation) error {
	if err := presentation.validate(); err != nil {
		return err
	}

	if registry == nil || service == nil || reflect.ValueOf(service).Kind() == reflect.Pointer && reflect.ValueOf(service).IsNil() || now == nil {
		return contract.ErrInvalidInput
	}

	handler := commandHandler{service: service, now: now, presentation: presentation}
	return registry.RegisterCommand("character", handler)
}

type commandHandler struct {
	presentation Presentation
	service      inspector
	now          func() time.Time
}

func (h commandHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	command, ok := personalCommand(event, h.now())
	if !ok {
		return interaction.HandlerResult{}, nil
	}

	if command.Name != "character" {
		return interaction.HandlerResult{}, nil
	}

	locale, err := h.presentation.Resolve(ctx, scope.DBTX(), command.Chat)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	translate := func(key localization.Key) (string, error) { return h.presentation.Catalog.Text(locale, key, nil) }
	if strings.TrimSpace(command.Arguments) != "" {
		text, err := translate("usage.character")
		if err != nil {
			return interaction.HandlerResult{}, err
		}

		return personalResponse(command, text), nil
	}

	snapshot, created, err := h.service.Ensure(ctx, scope.DBTX(), command.User)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	text, err := renderLocalizedSummary(h.presentation.Catalog, locale, snapshot, created)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	result := personalResponse(command, text)
	if h.presentation.Sharing != nil {
		response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
		response.Content, err = h.presentation.Sharing.Decorate(ctx, scope.DBTX(), sharingcontract.View{
			Owner: command.User, Destination: response.Destination,
			Kind: sharingcontract.CharacterSummary, Locale: locale, Body: response.Content,
		})
		if err != nil {
			return interaction.HandlerResult{}, err
		}

		result.ImmediateResponses[0] = response
	}
	if err := runtimecontract.ValidateResponse(result.ImmediateResponses[0]); err != nil {
		return interaction.HandlerResult{}, err
	}

	return result, nil
}

func personalCommand(event interaction.Event, now time.Time) (interaction.Command, bool) {
	command, ok := event.(interaction.Command)
	if pointer, isPointer := event.(*interaction.Command); isPointer && pointer != nil {
		command = *pointer
		ok = true
	}
	if !ok || command.Update <= 0 || command.User <= 0 || command.Chat >= 0 || command.ActorIsBot || command.SenderIsChat || command.EphemeralMessageID <= 0 || int64(int(command.EphemeralMessageID)) != int64(command.EphemeralMessageID) || command.SentAt.IsZero() {
		return interaction.Command{}, false
	}

	if command.ChatKind != runtimecontract.ChatKindGroup && command.ChatKind != runtimecontract.ChatKindSupergroup {
		return interaction.Command{}, false
	}

	if !now.Before(command.SentAt.Add(15 * time.Second)) {
		return interaction.Command{}, false
	}

	return command, true
}

func personalResponse(command interaction.Command, text string) interaction.HandlerResult {
	return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{runtimecontract.EphemeralResponse{
		Destination: runtimecontract.GroupDestination{ChatID: command.Chat, Kind: command.ChatKind}, TargetUserID: command.User,
		Authorization: runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: int(command.EphemeralMessageID), ValidUntil: command.SentAt.Add(15 * time.Second)}, Content: runtimecontract.MessageContent{Text: text},
	}}}
}
