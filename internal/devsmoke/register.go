// Package devsmoke supplies explicitly enabled development transport probes.
package devsmoke

import (
	"context"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/localization"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var errIncomingRequired = errors.New("devsmoke: incoming ephemeral authorization required")

// Register installs development transport behavior before the registry is sealed.
// Callers must explicitly opt in; production routing has no default smoke routes.
func Register(registry *interaction.Registry, now func() time.Time, p Presentation) error {
	if err := p.validate(); err != nil {
		return err
	}

	if registry == nil || now == nil {
		return errors.New("devsmoke: invalid registration")
	}

	h := smokeHandler{now: now, presentation: p}
	if err := registry.RegisterCommand("dev_smoke", h); err != nil {
		return err
	}

	if err := registry.RegisterCommand("dev_smoke_ephemeral", h); err != nil {
		return err
	}

	if err := registry.RegisterCallback("dev_smoke:", h); err != nil {
		return err
	}

	return registry.RegisterGroupMessages(h)
}

type smokeHandler struct {
	now          func() time.Time
	presentation Presentation
}

func (h smokeHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	var identity interaction.Identity

	switch e := event.(type) {
	case interaction.Command:
		if e.Name == "dev_smoke_ephemeral" && e.EphemeralMessageID <= 0 {
			return interaction.HandlerResult{}, errIncomingRequired
		}

		identity = e.Identity
	case interaction.Callback:
		identity = e.Identity
	case interaction.GroupMessage:
		identity = e.Identity
	default:
		return interaction.HandlerResult{}, nil
	}

	locale, err := h.presentation.Resolve(ctx, scope.DBTX(), identity.Chat)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	translate := func(key localization.Key) (string, error) {
		return h.presentation.Catalog.Text(locale, key, nil)
	}

	var authorization runtimecontract.EphemeralAuthorization

	text, err := translate("smoke.received")
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	switch e := event.(type) {
	case interaction.Command:
		identity = e.Identity
		if e.Name == "dev_smoke_ephemeral" {
			if e.EphemeralMessageID <= 0 {
				return interaction.HandlerResult{}, errIncomingRequired
			}

			authorization = runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: int(e.EphemeralMessageID), ValidUntil: h.now().Add(15 * time.Second)}
			text, err = translate("smoke.ephemeral")
			if err != nil {
				return interaction.HandlerResult{}, err
			}
		} else {
			public, err := translate("smoke.public")
			if err != nil {
				return interaction.HandlerResult{}, err
			}

			button, err := translate("smoke.button")
			if err != nil {
				return interaction.HandlerResult{}, err
			}

			response := runtimecontract.PublicResponse{Destination: destination(identity), Content: runtimecontract.MessageContent{Text: public, Buttons: [][]runtimecontract.InlineButton{{{Text: button, CallbackData: "dev_smoke:reply"}}}}}
			return result(response), nil
		}
	case interaction.Callback:
		identity = e.Identity
		authorization = runtimecontract.CallbackAuthorization{CallbackQueryID: string(e.QueryID), OriginatedFromEphemeral: e.EphemeralMessageID > 0, ValidUntil: h.now().Add(15 * time.Second)}
		text, err = translate("smoke.callback")
		if err != nil {
			return interaction.HandlerResult{}, err
		}
	case interaction.GroupMessage:
		identity = e.Identity
	}

	if authorization != nil {
		return result(runtimecontract.EphemeralResponse{Destination: destination(identity), TargetUserID: identity.User, Authorization: authorization, Content: runtimecontract.MessageContent{Text: text}}), nil
	}

	return result(runtimecontract.PublicResponse{Destination: destination(identity), Content: runtimecontract.MessageContent{Text: text}}), nil
}

func destination(identity interaction.Identity) runtimecontract.GroupDestination {
	return runtimecontract.GroupDestination{ChatID: identity.Chat, Kind: identity.ChatKind}
}

func result(response runtimecontract.ResponseRequest) interaction.HandlerResult {
	return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{response}}
}
