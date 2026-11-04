package devsmoke

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type smokeScope struct{}

func (smokeScope) DBTX() runtimecontract.DBTX { return nil }
func (smokeScope) EnqueueDelivery(context.Context, runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	panic("unexpected delivery")
}
func (smokeScope) Schedule(context.Context, runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	panic("unexpected job")
}
func TestLocalizedSmokeSurfaces(t *testing.T) {
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	calls := 0
	p := Presentation{Catalog: c, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		calls++
		return localization.Russian, nil
	}}
	registry := interaction.NewRegistry()
	if err := Register(registry, func() time.Time { return now }, p); err != nil {
		t.Fatal(err)
	}
	registry.Seal()
	id := interaction.Identity{Update: 1, Chat: -1, User: 1, ChatKind: runtimecontract.ChatKindGroup}
	for _, event := range []interaction.Event{interaction.Command{Identity: id, Name: "dev_smoke"}, interaction.Command{Identity: id, Name: "dev_smoke_ephemeral", EphemeralMessageID: 1}, interaction.Callback{Identity: id, Data: "dev_smoke:reply", QueryID: "query"}, interaction.GroupMessage{Identity: id, Text: "PRIVATE"}} {
		h, ok := registry.Resolve(event)
		if !ok {
			t.Fatal("route absent")
		}
		result, err := h.Handle(context.Background(), smokeScope{}, event)
		if err != nil {
			t.Fatal(err)
		}
		var content runtimecontract.MessageContent
		switch r := result.ImmediateResponses[0].(type) {
		case runtimecontract.PublicResponse:
			content = r.Content
		case runtimecontract.EphemeralResponse:
			content = r.Content
		}
		if !strings.HasPrefix(content.Text, "Проверка разработки:") || strings.Contains(content.Text, "PRIVATE") {
			t.Fatal(content.Text)
		}
		for _, row := range content.Buttons {
			for _, button := range row {
				if button.Text != "Личный ответ" {
					t.Fatal(button.Text)
				}
			}
		}
		if err := runtimecontract.ValidateResponseAt(result.ImmediateResponses[0], now); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 4 {
		t.Fatal("unexpected locale resolution count")
	}
}
