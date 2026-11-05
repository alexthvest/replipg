package sharing

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestSourceFingerprint(t *testing.T) {
	body := decorationView().Body
	body.Entities = append(body.Entities, runtimecontract.TextEntity{Kind: runtimecontract.TextEntityItalic, Offset: 10, Length: 3})
	a, err := fingerprint(body)
	if err != nil {
		t.Fatal(err)
	}

	body.Entities[0], body.Entities[1] = body.Entities[1], body.Entities[0]
	b, err := fingerprint(body)
	if err != nil || !bytes.Equal(a, b) {
		t.Fatal("entity order changes fingerprint")
	}

	body.Text = "😀 Health 101"
	b, err = fingerprint(body)
	if err != nil || bytes.Equal(a, b) {
		t.Fatal("changed text matched")
	}

	for _, entity := range []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 1, Length: 1}, {Kind: "underline", Offset: 3, Length: 1}, {Kind: runtimecontract.TextEntityBold, Offset: 3, Length: 1, URL: "unexpected"}, {Kind: runtimecontract.TextEntityBold, Offset: 100, Length: 1}} {
		body.Entities = []runtimecontract.TextEntity{entity}
		if _, err = fingerprint(body); err == nil {
			t.Fatal("accepted invalid entity")
		}
	}
}

func TestSourceAuthorizationAndBinding(t *testing.T) {
	now := time.Unix(100, 0).UTC()
	for _, name := range []string{"valid", "receiver absent", "foreign owner", "foreign group", "bot", "missing source", "missing keyboard", "body mismatch", "format mismatch", "receiver mismatch", "message mismatch", "first bind", "expired", "malformed token", "missing query", "ordinary source"} {
		t.Run(name, func(t *testing.T) {
			db := sharingTestDB(t)
			token := strings.Repeat("b", 32)
			s := decorationService(t, token, now)
			v := decorationView()
			v.EditMessageID = 7
			cb := interaction.Callback{Identity: interaction.Identity{Update: 1, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindSupergroup}, QueryID: "query", EphemeralMessageID: 7, Data: "share:v1:" + token, Source: &interaction.CallbackSource{Body: v.Body, CallbackData: []string{"share:v1:" + token}, ReceiverUserID: 42, EphemeralMessageID: 7}}
			cb.Source.Body.Buttons = nil
			want := sourceInvalid
			switch name {
			case "valid":
				want = sourceValid
			case "receiver absent":
				cb.Source.ReceiverUserID = 0
				want = sourceValid
			case "foreign owner":
				cb.User = 43
			case "foreign group":
				cb.Chat = -2
			case "bot":
				cb.ActorIsBot = true
			case "missing source":
				cb.Source = nil
				want = sourceUnavailable
			case "missing keyboard":
				cb.Source.CallbackData = nil
				want = sourceUnavailable
			case "body mismatch":
				cb.Source.Body.Text = "different"
				want = sourceUnavailable
			case "format mismatch":
				cb.Source.Body.Entities = []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityItalic, Offset: 3, Length: 6}}
				want = sourceUnavailable
			case "receiver mismatch":
				cb.Source.ReceiverUserID = 43
			case "message mismatch":
				cb.Source.EphemeralMessageID = 8
				cb.EphemeralMessageID = 8
				want = sourceUnavailable
			case "first bind":
				v.EditMessageID = 0
				want = sourceValid
			case "expired":
				v.Kind = "inventory_page"
				v.UsableUntil = now
				want = sourceExpired
			case "malformed token":
				cb.Data += "a"
			case "missing query":
				cb.QueryID = ""
			case "ordinary source":
				cb.EphemeralMessageID = 0
			}

			mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if _, err := s.Decorate(ctx, tx, v); err != nil {
					return err
				}

				stored, body, decision, err := verifySource(ctx, tx, cb, now)
				if err != nil {
					return err
				}

				if decision != want {
					t.Fatalf("decision %v want %v", decision, want)
				}

				if decision == sourceValid {
					if body.Text != v.Body.Text || len(body.Buttons) != 0 || stored.BoundMessageID != 7 {
						t.Fatal("verified source incorrect")
					}

					if name == "first bind" {
						if stored.Revision != 2 {
							t.Fatal("binding revision not reread")
						}

						cb.EphemeralMessageID = 8
						cb.Source.EphemeralMessageID = 8
						_, _, next, err := verifySource(ctx, tx, cb, now)
						if err != nil {
							return err
						}

						if next == sourceValid {
							t.Fatal("rebound source")
						}
					}
				}

				return nil
			})
		})
	}
}
