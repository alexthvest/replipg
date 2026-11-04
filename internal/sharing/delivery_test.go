package sharing

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func publicFixture() PublicPayload {
	return PublicPayload{ActionUpdateID: 42, Destination: runtimecontract.GroupDestination{ChatID: -10, Kind: runtimecontract.ChatKindSupergroup}, Content: runtimecontract.MessageContent{Text: "Old snapshot 😀\nOwner: 123", Entities: []runtimecontract.TextEntity{{Kind: runtimecontract.TextEntityBold, Offset: 0, Length: 3}}}}
}
func feedbackFixture() FeedbackPayload {
	p := publicFixture()
	return FeedbackPayload{ActionUpdateID: p.ActionUpdateID, Destination: p.Destination, TargetUserID: 123, CallbackQueryID: "callback", ValidUntilMS: 1700000015000, Content: runtimecontract.MessageContent{Text: "Shared."}}
}
func TestDeliveryFrozenRoundTrip(t *testing.T) {
	p := publicFixture()
	wire, err := EncodePublic(p)
	if err != nil {
		t.Fatal(err)
	}
	p.Content.Text = "changed"
	p.Content.Entities[0].Length = 1
	got, err := DecodePublic(wire)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, runtimecontract.PublicResponse{Destination: publicFixture().Destination, Content: publicFixture().Content}) {
		t.Fatalf("snapshot changed: %#v", got)
	}
	if strings.Contains(string(wire.Data()), "callback_query_id") || strings.Contains(string(wire.Data()), "target_user_id") {
		t.Fatal("public work contains personal authorization")
	}
	f := feedbackFixture()
	wire, err = EncodeFeedback(f)
	if err != nil {
		t.Fatal(err)
	}
	got, err = DecodeFeedback(wire)
	if err != nil {
		t.Fatal(err)
	}
	response := got.(runtimecontract.EphemeralResponse)
	auth, ok := response.Authorization.(runtimecontract.CallbackAuthorization)
	if !ok || auth.CallbackQueryID != f.CallbackQueryID || !auth.OriginatedFromEphemeral || auth.ValidUntil.UnixMilli() != f.ValidUntilMS || response.TargetUserID != f.TargetUserID || response.EditMessageID != 0 || response.ReplaceCallbackMessage {
		t.Fatalf("wrong authorization: %#v", response)
	}
	if err := runtimecontract.ValidateResponseAt(response, time.UnixMilli(f.ValidUntilMS)); err == nil {
		t.Fatal("fixed expiry renewed")
	}
}
func TestDeliveryRejectsMalformedPayloads(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*PublicPayload)
	}{
		{"missing action", func(p *PublicPayload) { p.ActionUpdateID = 0 }},
		{"negative action", func(p *PublicPayload) { p.ActionUpdateID = -1 }},
		{"private destination", func(p *PublicPayload) { p.Destination.ChatID = 1 }},
		{"empty content", func(p *PublicPayload) { p.Content.Text = "" }},
		{"bad entities", func(p *PublicPayload) { p.Content.Entities[0].Offset = 999 }},
		{"oversize", func(p *PublicPayload) { p.Content.Text = strings.Repeat("😀", 2049) }},
		{"buttons", func(p *PublicPayload) {
			p.Content.Buttons = [][]runtimecontract.InlineButton{{{Text: "Share", CallbackData: "share"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := publicFixture()
			tc.mutate(&p)
			data, _ := json.Marshal(p)
			wire, _ := runtimecontract.NewVersionedPayload(1, data)
			if _, err := DecodePublic(wire); err == nil {
				t.Fatal("invalid public payload accepted")
			}
			if _, err := EncodePublic(p); err == nil {
				t.Fatal("invalid public payload encoded")
			}
		})
	}
	for _, mutate := range []func(*FeedbackPayload){func(p *FeedbackPayload) { p.TargetUserID = 0 }, func(p *FeedbackPayload) { p.CallbackQueryID = "" }, func(p *FeedbackPayload) { p.ValidUntilMS = 0 }, func(p *FeedbackPayload) { p.ValidUntilMS = -1 }, func(p *FeedbackPayload) { p.ActionUpdateID = 0 }, func(p *FeedbackPayload) { p.Destination.ChatID = 1 }} {
		p := feedbackFixture()
		mutate(&p)
		data, _ := json.Marshal(p)
		wire, _ := runtimecontract.NewVersionedPayload(1, data)
		if _, err := DecodeFeedback(wire); err == nil {
			t.Fatal("invalid feedback accepted")
		}
		if _, err := EncodeFeedback(p); err == nil {
			t.Fatal("invalid feedback encoded")
		}
	}
	valid, _ := json.Marshal(publicFixture())
	for _, tc := range []struct {
		version uint16
		data    string
	}{{2, string(valid)}, {1, "{}"}, {1, string(valid) + " {}"}, {1, `{"action_update_id":42,"authorization":{}}`}, {1, `null`}, {1, `{"action_update_id":"42"}`}} {
		wire, _ := runtimecontract.NewVersionedPayload(tc.version, []byte(tc.data))
		if _, err := DecodePublic(wire); err == nil {
			t.Fatal("malformed payload accepted")
		}
		if _, err := DecodeFeedback(wire); err == nil {
			t.Fatal("malformed feedback accepted")
		}
	}
}
func TestSharingCatalog(t *testing.T) {
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []localization.Locale{localization.English, localization.Russian} {
		for _, key := range []localization.Key{"sharing.share", "sharing.pending", "sharing.success", "sharing.failure", "sharing.unknown", "sharing.unknown_retry", "sharing.unavailable", "sharing.reopen"} {
			if text, err := c.Text(locale, key, nil); err != nil || text == "" {
				t.Fatalf("%s %s: %q %v", locale, key, text, err)
			}
		}
		for _, tc := range []struct {
			key   localization.Key
			param string
		}{{"sharing.attribution", "user_id"}, {"sharing.cooldown", "seconds"}} {
			text, err := c.Text(locale, tc.key, localization.Args{tc.param: localization.Integer(123)})
			if err != nil || !strings.Contains(text, "123") {
				t.Fatalf("%s: %q %v", tc.key, text, err)
			}
			for _, args := range []localization.Args{nil, {tc.param: localization.String("123")}, {tc.param: localization.Integer(123), "extra": localization.Integer(1)}} {
				if _, err := c.Text(locale, tc.key, args); err == nil {
					t.Fatal("invalid localization parameters accepted")
				}
			}
		}
	}
}

func TestDeliveryRequestsAreSingleAttempt(t *testing.T) {
	public, err := NewPublicRequest(publicFixture())
	if err != nil {
		t.Fatal(err)
	}
	feedback, err := NewFeedbackRequest(feedbackFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range []runtimecontract.DeliveryRequest{public, feedback} {
		if err := req.Validate(); err != nil {
			t.Fatal(err)
		}
		if req.Payload.Version() != PayloadVersion || req.Retry.MaxAttempts != 1 || req.Retry.InitialDelay <= 0 || req.Retry.MaxDelay < req.Retry.InitialDelay {
			t.Fatalf("unsafe delivery policy: %#v", req.Retry)
		}
	}
	if public.Kind != PublicKind || feedback.Kind != FeedbackKind {
		t.Fatal("wrong delivery kinds")
	}
	if _, err := NewPublicRequest(PublicPayload{}); err == nil {
		t.Fatal("invalid public request accepted")
	}
	if _, err := NewFeedbackRequest(FeedbackPayload{}); err == nil {
		t.Fatal("invalid feedback request accepted")
	}
}

func TestAttributionUsesFrozenLocaleAndNumericOwner(t *testing.T) {
	catalog, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}
	en, err := Attribution(catalog, localization.English, 123)
	if err != nil || en != "Shared by user 123" {
		t.Fatalf("%q %v", en, err)
	}
	ru, err := Attribution(catalog, localization.Russian, 123)
	if err != nil || ru != "Поделился пользователь 123" {
		t.Fatalf("%q %v", ru, err)
	}
	for _, owner := range []runtimecontract.UserID{0, -1} {
		if _, err := Attribution(catalog, localization.English, owner); err == nil {
			t.Fatal("invalid owner accepted")
		}
	}
	if _, err := Attribution(catalog, "unknown", 123); err == nil {
		t.Fatal("unsupported locale accepted")
	}
	if _, err := Attribution(nil, localization.English, 123); err == nil {
		t.Fatal("missing catalog accepted")
	}
}

func TestDeliveryRejectsExtraTransportCapabilities(t *testing.T) {
	public, err := EncodePublic(publicFixture())
	if err != nil {
		t.Fatal(err)
	}
	feedback, err := EncodeFeedback(feedbackFixture())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		original runtimecontract.VersionedPayload
		field    string
		decode   func(runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error)
	}{
		{"public authorization", public, `"authorization":{},`, DecodePublic},
		{"public receiver", public, `"target_user_id":123,`, DecodePublic},
		{"public reply", public, `"reply_to_message_id":1,`, DecodePublic},
		{"feedback interface", feedback, `"authorization":{},`, DecodeFeedback},
		{"feedback edit", feedback, `"edit_message_id":1,`, DecodeFeedback},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := strings.Replace(string(tc.original.Data()), "{", "{"+tc.field, 1)
			wire, err := runtimecontract.NewVersionedPayload(PayloadVersion, []byte(data))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tc.decode(wire); err == nil {
				t.Fatal("additional transport capability accepted")
			}
		})
	}
}
