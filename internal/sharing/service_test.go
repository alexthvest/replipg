package sharing

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/sharing/contract"
)

func decorationService(t *testing.T, token string, now time.Time) *Service {
	t.Helper()
	catalog, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	s, err := NewService(catalog, ServiceOptions{Now: func() time.Time { return now }, NewToken: func() (string, error) { return token, nil }})
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func decorationView() contract.View {
	return contract.View{Owner: 42, Destination: runtimecontract.GroupDestination{ChatID: -1, Kind: runtimecontract.ChatKindSupergroup}, Kind: contract.CharacterSummary, Locale: localization.English, Body: runtimecontract.MessageContent{Text: "😀 Health 100", Entities: []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 3, Length: 6}}, Buttons: [][]runtimecontract.InlineButton{{{Text: "Back", CallbackData: "inventory:back"}}}}}
}

func TestDecorationPreservesViewsAndMetadata(t *testing.T) {
	for _, kind := range []contract.ViewKind{contract.CharacterSummary, contract.InventoryPage, contract.ItemDetails} {
		t.Run(string(kind), func(t *testing.T) {
			db := sharingTestDB(t)
			now := time.Unix(100, 0).UTC()
			token := strings.Repeat("a", 32)
			s := decorationService(t, token, now)
			v := decorationView()
			v.Kind = kind
			if kind != contract.CharacterSummary {
				v.UsableUntil = now.Add(5 * time.Minute)
				v.EditMessageID = 7
			}
			mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				got, err := s.Decorate(ctx, tx, v)
				if err != nil {
					return err
				}

				if got.Text != v.Body.Text || !reflect.DeepEqual(got.Entities, v.Body.Entities) || len(got.Buttons) != 2 || !reflect.DeepEqual(got.Buttons[0], v.Body.Buttons[0]) {
					t.Fatal("decoration must preserve body and append one Share row")
				}

				if got.Buttons[1][0].CallbackData != "share:v1:"+token || got.Buttons[1][0].Text != "Share" || got.Buttons[1][0].Style != runtimecontract.ButtonPrimary {
					t.Fatal("invalid Share control")
				}

				stored, err := readView(ctx, tx, token)
				if err != nil {
					return err
				}

				if stored.BoundMessageID != v.EditMessageID || !stored.UsableUntil.Equal(v.UsableUntil) || stored.Locale != v.Locale || len(stored.BodyHash) != 32 {
					t.Fatal("metadata changed")
				}

				got.Entities[0].Length = 1
				got.Buttons[0][0].Text = "mutated"

				if v.Body.Entities[0].Length != 6 || v.Body.Buttons[0][0].Text != "Back" {
					t.Fatal("decoration aliases caller content")
				}

				var count int

				if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sharing_actions").Scan(&count); err != nil {
					return err
				}

				if count != 0 {
					t.Fatal("decoration requested publication")
				}

				return nil
			})
		})
	}
}

func TestDecorationFreshRandomTokensAndRollback(t *testing.T) {
	db := sharingTestDB(t)
	catalog, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}

	s, err := NewService(catalog, ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}

	v := decorationView()
	v.Locale = localization.Russian
	tokens := map[string]bool{}
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for range 2 {
			body, err := s.Decorate(ctx, tx, v)
			if err != nil {
				return err
			}

			control := body.Buttons[1][0]
			token, ok := parseShareToken(control.CallbackData)

			if !ok || tokens[token] {
				t.Fatal("token not fresh 128-bit lowercase hex")
			}

			tokens[token] = true
			label, err := catalog.Text(localization.Russian, localization.SharingShare, nil)
			if err != nil {
				return err
			}

			if control.Text != label {
				t.Fatal("Share label is not localized")
			}
		}
		return nil
	})
	rollback := errors.New("rollback")
	err = db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := s.Decorate(ctx, tx, v); err != nil {
			return err
		}

		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}

	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var views, actions int

		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sharing_views").Scan(&views); err != nil {
			return err
		}

		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sharing_actions").Scan(&actions); err != nil {
			return err
		}

		if views != 2 || actions != 0 {
			t.Fatal("decoration committed independently or requested publication")
		}

		return nil
	})
}

func TestDecorationRejectsInvalidViews(t *testing.T) {
	db := sharingTestDB(t)
	now := time.Unix(100, 0).UTC()
	for _, name := range []string{"owner", "group", "kind", "locale", "character deadline", "inventory deadline", "edit", "body", "token", "random failure"} {
		t.Run(name, func(t *testing.T) {
			s := decorationService(t, strings.Repeat("a", 32), now)
			v := decorationView()
			switch name {
			case "owner":
				v.Owner = 0
			case "group":
				v.Destination.ChatID = 1
			case "kind":
				v.Kind = "settings"
			case "locale":
				v.Locale = "xx"
			case "character deadline":
				v.UsableUntil = now
			case "inventory deadline":
				v.Kind = contract.InventoryPage
			case "edit":
				v.EditMessageID = -1
			case "body":
				v.Body.Entities[0].Offset = 1
			case "token":
				s.newToken = func() (string, error) { return strings.Repeat("A", 32), nil }
			case "random failure":
				s.newToken = func() (string, error) { return "", errors.New("private entropy detail") }
			}

			mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if _, err := s.Decorate(ctx, tx, v); err == nil {
					t.Fatal("accepted invalid view")
				} else if strings.Contains(err.Error(), "private entropy detail") {
					t.Fatal("unsafe entropy diagnostic")
				}
				return nil
			})
		})
	}

	if _, err := NewService(nil, ServiceOptions{}); err == nil {
		t.Fatal("nil catalog accepted")
	}
}
