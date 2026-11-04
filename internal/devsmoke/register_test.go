package devsmoke

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestRegisterTransportBehaviors(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	registry := interaction.NewRegistry()
	if err := Register(registry, func() time.Time { return now }, englishPresentation(t)); err != nil {
		t.Fatal(err)
	}
	registry.Seal()
	identity := interaction.Identity{Update: 1, Chat: -100, User: 8, ChatKind: runtimecontract.ChatKindSupergroup}
	for _, event := range []interaction.Event{
		interaction.Command{Identity: identity, Name: "dev_smoke"},
		interaction.Callback{Identity: identity, QueryID: "query", Data: "dev_smoke:reply"},
		interaction.Command{Identity: identity, Name: "dev_smoke_ephemeral", EphemeralMessageID: 77},
		interaction.GroupMessage{Identity: identity, Text: "PRIVATE incoming body"},
	} {
		handler, ok := registry.Resolve(event)
		if !ok {
			t.Fatal("development route absent")
		}
		result, err := handler.Handle(context.Background(), smokeScope{}, event)
		if err != nil || len(result.ImmediateResponses) != 1 {
			t.Fatal("development response absent")
		}
		if err = runtimecontract.ValidateResponseAt(result.ImmediateResponses[0], now); err != nil {
			t.Fatal(err)
		}
		if response, ok := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse); ok {
			switch auth := response.Authorization.(type) {
			case runtimecontract.CallbackAuthorization:
				if auth.ValidUntil != now.Add(15*time.Second) || auth.CallbackQueryID != "query" {
					t.Fatal("invalid callback evidence")
				}
			case runtimecontract.IncomingEphemeralAuthorization:
				if auth.ValidUntil != now.Add(15*time.Second) || auth.EphemeralMessageID != 77 {
					t.Fatal("invalid incoming evidence")
				}
			default:
				t.Fatal("unexpected authorization")
			}
		}
	}
}

func TestIncomingProbeRequiresEphemeralEvidence(t *testing.T) {
	registry := interaction.NewRegistry()
	if err := Register(registry, time.Now, englishPresentation(t)); err != nil {
		t.Fatal(err)
	}
	registry.Seal()
	event := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -100, User: 8, ChatKind: runtimecontract.ChatKindGroup}, Name: "dev_smoke_ephemeral", MessageID: 9}
	handler, ok := registry.Resolve(event)
	if !ok {
		t.Fatal("probe not registered")
	}
	result, err := handler.Handle(context.Background(), smokeScope{}, event)
	if err == nil || len(result.ImmediateResponses) != 0 {
		t.Fatal("ordinary message supplied ephemeral authorization")
	}
}

func TestRegisterReportsRouteOwnershipConflict(t *testing.T) {
	registry := interaction.NewRegistry()
	if err := Register(registry, time.Now, englishPresentation(t)); err != nil {
		t.Fatal(err)
	}
	if err := Register(registry, time.Now, englishPresentation(t)); !errors.Is(err, interaction.ErrRouteConflict) {
		t.Fatal("route conflict not reported")
	}
	registry.Seal()
	if err := Register(registry, time.Now, englishPresentation(t)); !errors.Is(err, interaction.ErrRegistrationClosed) {
		t.Fatal("sealed registration not reported")
	}
}
