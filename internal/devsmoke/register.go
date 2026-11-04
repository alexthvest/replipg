// Package devsmoke supplies explicitly enabled development transport probes.
package devsmoke

import (
	"context"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

var errIncomingRequired = errors.New("devsmoke: incoming ephemeral authorization required")

// Register installs development transport behavior before the registry is sealed.
// Callers must explicitly opt in; production routing has no default smoke routes.
func Register(registry *interaction.Registry, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	handler := smokeHandler{now: now}
	if err := registry.RegisterCommand("dev_smoke", handler); err != nil {
		return err
	}
	if err := registry.RegisterCommand("dev_smoke_ephemeral", handler); err != nil {
		return err
	}
	if err := registry.RegisterCallback("dev_smoke:", handler); err != nil {
		return err
	}
	return registry.RegisterGroupMessages(handler)
}

type smokeHandler struct{ now func() time.Time }

func (h smokeHandler) Handle(_ context.Context, _ runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	var identity interaction.Identity
	var authorization runtimecontract.EphemeralAuthorization
	text := "Development smoke: ordinary group message received."
	switch e := event.(type) {
	case interaction.Command:
		identity = e.Identity
		if e.Name == "dev_smoke_ephemeral" {
			if e.EphemeralMessageID <= 0 {
				return interaction.HandlerResult{}, errIncomingRequired
			}
			authorization = runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: int(e.EphemeralMessageID), ValidUntil: h.now().Add(15 * time.Second)}
			text = "Development smoke: restricted ephemeral reply."
		} else {
			response := runtimecontract.PublicResponse{Destination: destination(identity), Content: runtimecontract.MessageContent{Text: "Development smoke: public response.", Buttons: [][]runtimecontract.InlineButton{{{Text: "Restricted reply", CallbackData: "dev_smoke:reply"}}}}}
			return result(response), nil
		}
	case interaction.Callback:
		identity = e.Identity
		authorization = runtimecontract.CallbackAuthorization{CallbackQueryID: string(e.QueryID), OriginatedFromEphemeral: e.EphemeralMessageID > 0, ValidUntil: h.now().Add(15 * time.Second)}
		text = "Development smoke: restricted callback response."
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
