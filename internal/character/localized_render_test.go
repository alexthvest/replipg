package character

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestLocalizedCharacterSummaryAndSelection(t *testing.T) {
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	s := contract.Snapshot{Health: 0, MaxHealth: 100, Attack: 10, Defense: 5, State: contract.StateIncapacitated, RecoverAt: time.Date(2026, 10, 28, 1, 2, 3, 0, time.FixedZone("fixture", 3600))}
	text, err := renderLocalizedSummary(c, localization.Russian, s, true)
	if err != nil {
		t.Fatal(err)
	}

	for _, part := range []string{"Персонаж создан.", "Здоровье: 0/100", "Атака: 10", "Защита: 5", "недееспособен", "2026-10-28T00:02:03Z"} {
		if !strings.Contains(text, part) {
			t.Fatalf("missing %q in %q", part, text)
		}
	}

	service := &fakeInspector{created: true}
	calls := 0
	p := Presentation{Catalog: c, Resolve: func(_ context.Context, _ runtimecontract.DBTX, chat runtimecontract.ChatID) (localization.Locale, error) {
		calls++
		if chat == -100 {
			return localization.Russian, nil
		}

		return localization.English, nil
	}}
	registry := interaction.NewRegistry()
	if err := Register(registry, service, func() time.Time { return commandNow }, p); err != nil {
		t.Fatal(err)
	}

	registry.Seal()
	cmd := authorizedCommand()
	for _, chat := range []runtimecontract.ChatID{-100, -200} {
		cmd.Chat = chat
		handler, _ := registry.Resolve(cmd)
		result, err := handler.Handle(context.Background(), commandScope{}, cmd)
		if err != nil {
			t.Fatal(err)
		}

		response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
		want := "Health:"
		if chat == -100 {
			want = "Здоровье:"
		}

		if !strings.Contains(response.Content.Text, want) {
			t.Fatal(response.Content.Text)
		}
	}

	if calls != 2 {
		t.Fatal("resolve must run once per interaction")
	}

	failure := errors.New("safe storage fixture")
	p.Resolve = func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return "", failure
	}
	h := commandHandler{service: service, now: func() time.Time { return commandNow }, presentation: p}
	before := service.calls
	if _, err := h.Handle(context.Background(), commandScope{}, cmd); !errors.Is(err, failure) || service.calls != before {
		t.Fatal("resolver error defaulted or mutated character")
	}

	if err := Register(interaction.NewRegistry(), service, time.Now, Presentation{}); err == nil {
		t.Fatal("missing dependencies accepted")
	}
}

func TestLocalizedCharacterDevelopmentUsageAndRejection(t *testing.T) {
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	p := Presentation{Catalog: c, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return localization.Russian, nil
	}}
	service := &fakeHealthService{mutationErr: contract.ErrIncapacitated}
	h := developmentHandler{service: service, now: func() time.Time { return commandNow }, presentation: p}
	cmd := authorizedCommand()
	cmd.Name = "dev_character_damage"
	for _, tc := range []struct{ args, want string }{{"-1", "Использование:"}, {"5", "Ваш персонаж недееспособен"}} {
		cmd.Arguments = tc.args
		result, err := h.Handle(context.Background(), commandScope{}, cmd)
		if err != nil {
			t.Fatal(err)
		}

		response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

		if !strings.Contains(response.Content.Text, tc.want) {
			t.Fatal(response.Content.Text)
		}
	}
}
