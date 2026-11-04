package character_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	sharingcontract "github.com/alexthvest/replipg/internal/sharing/contract"
)

type capturingCharacterDecorator struct {
	views []sharingcontract.View
	tx    runtimecontract.DBTX
	err   error
}

func (d *capturingCharacterDecorator) Decorate(_ context.Context, tx runtimecontract.DBTX, view sharingcontract.View) (runtimecontract.MessageContent, error) {
	d.views = append(d.views, view)
	d.tx = tx
	body := view.Body
	body.Buttons = append(body.Buttons, []runtimecontract.InlineButton{{Text: "Share", CallbackData: "share:v1:0123456789abcdef0123456789abcdef"}})
	return body, d.err
}

func TestCharacterSharingPresentation(t *testing.T) {
	for _, locale := range []localization.Locale{localization.English, localization.Russian} {
		t.Run(string(locale), func(t *testing.T) {
			service, db, _ := serviceFixture(t)
			catalog, err := localization.Load()
			if err != nil {
				t.Fatal(err)
			}

			now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
			decorator := &capturingCharacterDecorator{}
			resolveCalls := 0
			p := character.Presentation{Catalog: catalog, Sharing: decorator, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
				resolveCalls++
				if resolveCalls == 1 {
					return locale, nil
				}

				// A subsequent settings read must not replace the locale of the rendered body.
				return "invalid-new-selection", nil
			}}
			routes := interaction.NewRegistry()
			if err := character.Register(routes, service, func() time.Time { return now }, p); err != nil {
				t.Fatal(err)
			}

			routes.Seal()
			cmd := interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -100, User: 7, ChatKind: runtimecontract.ChatKindSupergroup}, Name: "character", SentAt: now, EphemeralMessageID: 23}
			handler, ok := routes.Resolve(cmd)

			if !ok {
				t.Fatal("missing command")
			}

			inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				result, err := handler.Handle(ctx, developmentScope{tx}, cmd)
				if err != nil {
					return err
				}

				if len(decorator.views) != 1 {
					t.Fatalf("summary decoration calls = %d, want 1", len(decorator.views))
				}

				view := decorator.views[0]
				response, ok := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

				if !ok || len(result.ImmediateResponses) != 1 {
					t.Fatal("summary must remain personal")
				}

				if decorator.tx != tx || view.Owner != 7 || view.Destination != response.Destination || view.Kind != sharingcontract.CharacterSummary || view.Locale != locale || !view.UsableUntil.IsZero() || view.EditMessageID != 0 {
					t.Fatalf("incorrect view metadata: %+v", view)
				}

				want := "Health: 100/100"
				if locale == localization.Russian {
					want = "Здоровье: 100/100"
				}

				if !strings.Contains(view.Body.Text, want) || view.Body.Text != response.Content.Text || !reflect.DeepEqual(view.Body.Entities, response.Content.Entities) || len(view.Body.Buttons) != 0 || len(response.Content.Buttons) != 1 {
					t.Fatal("final body or decorated controls lost")
				}

				auth, ok := response.Authorization.(runtimecontract.IncomingEphemeralAuthorization)

				if response.TargetUserID != 7 || !ok || auth.EphemeralMessageID != 23 || !auth.ValidUntil.Equal(now.Add(15*time.Second)) {
					t.Fatal("personal authorization changed")
				}

				return runtimecontract.ValidateResponseAt(response, now)
			})

			if resolveCalls != 1 {
				t.Fatalf("locale resolved %d times", resolveCalls)
			}
		})
	}
}

func TestCharacterSharingFailureRollsBackCreation(t *testing.T) {
	service, db, _ := serviceFixture(t)
	catalog, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	failure := errors.New("decoration storage failure")
	decorator := &capturingCharacterDecorator{err: failure}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	p := character.Presentation{Catalog: catalog, Sharing: decorator, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return localization.English, nil
	}}
	routes := interaction.NewRegistry()
	if err := character.Register(routes, service, func() time.Time { return now }, p); err != nil {
		t.Fatal(err)
	}

	routes.Seal()
	cmd := interaction.Command{Identity: interaction.Identity{Update: 2, Chat: -100, User: 7, ChatKind: runtimecontract.ChatKindSupergroup}, Name: "character", SentAt: now, EphemeralMessageID: 23}
	handler, _ := routes.Resolve(cmd)
	err = db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := handler.Handle(ctx, developmentScope{tx}, cmd)

		if len(result.ImmediateResponses) != 0 {
			t.Fatal("failed decoration produced response")
		}

		return err
	})
	if !errors.Is(err, failure) {
		t.Fatalf("decoration failure not propagated: %v", err)
	}

	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, found, err := service.Lookup(ctx, tx, 7)

		if found {
			t.Fatal("creation escaped failed decoration transaction")
		}

		return err
	})
}

func TestCharacterSharingExcludesUsageAndDevelopment(t *testing.T) {
	service, db, _ := serviceFixture(t)
	catalog, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	decorator := &capturingCharacterDecorator{}
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	p := character.Presentation{Catalog: catalog, Sharing: decorator, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return localization.English, nil
	}}
	routes := interaction.NewRegistry()
	if err := character.Register(routes, service, func() time.Time { return now }, p); err != nil {
		t.Fatal(err)
	}

	if err := character.RegisterDevelopment(routes, service, func() time.Time { return now }, p); err != nil {
		t.Fatal(err)
	}

	routes.Seal()
	for _, tc := range []struct {
		name interaction.CommandName
		args string
	}{{"character", "unexpected"}, {"dev_character_damage", "1"}, {"dev_character_defeat", ""}} {
		inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
			cmd := interaction.Command{Identity: interaction.Identity{Update: 3, Chat: -100, User: 7, ChatKind: runtimecontract.ChatKindSupergroup}, Name: tc.name, Arguments: tc.args, SentAt: now, EphemeralMessageID: 23}
			handler, _ := routes.Resolve(cmd)
			result, err := handler.Handle(ctx, developmentScope{tx}, cmd)
			if err != nil {
				return err
			}

			response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)

			if len(response.Content.Buttons) != 0 || len(decorator.views) != 0 {
				t.Fatal("unsupported view decorated")
			}

			return nil
		})
	}
}
