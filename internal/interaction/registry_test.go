package interaction

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type recordingHandler struct {
	calls int
	event Event
}

func (h *recordingHandler) Handle(_ context.Context, _ runtimecontract.WorkScope, event Event) (HandlerResult, error) {
	h.calls++
	h.event = event
	return HandlerResult{}, nil
}

func testIdentity() Identity {
	return Identity{Update: 42, Chat: -100, User: 17, ChatKind: runtimecontract.ChatKindSupergroup}
}

func TestRegistryResolvesOnlyRegisteredOwners(t *testing.T) {
	registry := NewRegistry()
	command, callback, message := &recordingHandler{}, &recordingHandler{}, &recordingHandler{}
	for _, err := range []error{
		registry.RegisterCommand("feature_action", command),
		registry.RegisterCallback("feature:", callback),
		registry.RegisterGroupMessages(message),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	registry.Seal()
	for _, tt := range []struct {
		name  string
		event Event
		want  Handler
	}{
		{"command", Command{Identity: testIdentity(), Name: "feature_action", Arguments: "opaque args", MessageID: 7}, command},
		{"callback", Callback{Identity: testIdentity(), QueryID: "query", Data: "feature:opaque"}, callback},
		{"callback prefix itself", Callback{Identity: testIdentity(), Data: "feature:"}, callback},
		{"message", GroupMessage{Identity: testIdentity(), MessageID: 7, Text: "opaque text"}, message},
		{"unregistered command", Command{Identity: testIdentity(), Name: "other"}, nil},
		{"unregistered callback", Callback{Identity: testIdentity(), Data: "other:opaque"}, nil},
		{"near prefix", Callback{Identity: testIdentity(), Data: "feature"}, nil},
		{"nil", nil, nil},
		{"typed nil", (*Command)(nil), nil},
		{"typed nil callback", (*Callback)(nil), nil},
		{"typed nil message", (*GroupMessage)(nil), nil},
		{"unsupported", struct{ Event }{}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := registry.Resolve(tt.event)

			if got != tt.want || ok != (tt.want != nil) {
				t.Fatalf("Resolve() = (%v, %v), want (%v, %v)", got, ok, tt.want, tt.want != nil)
			}
		})
	}

	if command.calls+callback.calls+message.calls != 0 {
		t.Fatal("route resolution invoked a handler")
	}
}

func TestRegistryRejectsAmbiguousOwnership(t *testing.T) {
	for _, tt := range []struct {
		name     string
		register func(*Registry, Handler) error
		conflict func(*Registry, Handler) error
	}{
		{"command", func(r *Registry, h Handler) error { return r.RegisterCommand("feature", h) }, func(r *Registry, h Handler) error { return r.RegisterCommand("feature", h) }},
		{"duplicate prefix", func(r *Registry, h Handler) error { return r.RegisterCallback("feature:", h) }, func(r *Registry, h Handler) error { return r.RegisterCallback("feature:", h) }},
		{"nested prefix", func(r *Registry, h Handler) error { return r.RegisterCallback("feature:", h) }, func(r *Registry, h Handler) error { return r.RegisterCallback("feature:child:", h) }},
		{"parent prefix", func(r *Registry, h Handler) error { return r.RegisterCallback("feature:child:", h) }, func(r *Registry, h Handler) error { return r.RegisterCallback("feature:", h) }},
		{"ordinary messages", func(r *Registry, h Handler) error { return r.RegisterGroupMessages(h) }, func(r *Registry, h Handler) error { return r.RegisterGroupMessages(h) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			owner := &recordingHandler{}
			if err := tt.register(r, owner); err != nil {
				t.Fatal(err)
			}

			for _, contender := range []Handler{&recordingHandler{}, owner} {
				if err := tt.conflict(r, contender); !errors.Is(err, ErrRouteConflict) {
					t.Fatalf("conflict = %v, want ErrRouteConflict", err)
				}
			}
		})
	}
}

func TestRegistryRejectsInvalidRegistrationsWithoutReservingRoutes(t *testing.T) {
	r := NewRegistry()
	h := &recordingHandler{}
	for _, name := range []CommandName{"", "/feature", "feature@bot", "Feature", "a b", "é", CommandName(strings.Repeat("a", 33))} {
		if err := r.RegisterCommand(name, h); !errors.Is(err, ErrInvalidRoute) {
			t.Errorf("RegisterCommand(%q) = %v", name, err)
		}
	}

	for _, prefix := range []CallbackPrefix{"", CallbackPrefix(strings.Repeat("a", 65)), "\xff"} {
		if err := r.RegisterCallback(prefix, h); !errors.Is(err, ErrInvalidRoute) {
			t.Errorf("RegisterCallback(%q) = %v", prefix, err)
		}
	}

	for _, handler := range []Handler{nil, (*recordingHandler)(nil)} {
		for _, err := range []error{r.RegisterCommand("valid", handler), r.RegisterCallback("valid:", handler), r.RegisterGroupMessages(handler)} {
			if !errors.Is(err, ErrInvalidHandler) {
				t.Errorf("nil handler = %v", err)
			}
		}
	}

	for _, err := range []error{r.RegisterCommand("valid", h), r.RegisterCallback("valid:", h), r.RegisterCallback("valid_other:", h), r.RegisterGroupMessages(h)} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRegistrySealsBeforeReadinessAndHasNoBuiltInRoutes(t *testing.T) {
	var r Registry // The zero value is usable.
	h := &recordingHandler{}
	r.Seal()
	r.Seal()
	for _, err := range []error{r.RegisterCommand("feature", h), r.RegisterCallback("feature:", h), r.RegisterGroupMessages(h)} {
		if !errors.Is(err, ErrRegistrationClosed) {
			t.Errorf("registration after Seal() = %v", err)
		}
	}

	for _, event := range []Event{Command{Identity: testIdentity(), Name: "feature"}, Callback{Identity: testIdentity(), Data: "feature:"}, GroupMessage{Identity: testIdentity(), Text: "anything"}} {
		if handler, ok := r.Resolve(event); handler != nil || ok {
			t.Fatalf("empty registry resolved %T", event)
		}
	}
}

func TestRegistryDoesNotAdmitEventsUntilSealed(t *testing.T) {
	r := NewRegistry()
	h := &recordingHandler{}
	if err := r.RegisterCommand("feature", h); err != nil {
		t.Fatal(err)
	}

	event := &Command{Identity: testIdentity(), Name: "feature"}
	if owner, ok := r.Resolve(event); owner != nil || ok {
		t.Fatal("event admitted before registration closed")
	}

	r.Seal()
	if owner, ok := r.Resolve(event); owner != h || !ok {
		t.Fatal("sealed event did not resolve")
	}
}

func TestConcurrentRegistrationHasOneOwner(t *testing.T) {
	r := NewRegistry()
	const contenders = 16
	var wg sync.WaitGroup

	results := make(chan error, contenders)
	for range contenders {
		wg.Go(func() { results <- r.RegisterCallback("feature:", &recordingHandler{}) })
	}

	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrRouteConflict) {
			t.Fatal(err)
		}
	}

	if winners != 1 {
		t.Fatalf("owners = %d, want 1", winners)
	}

	r.Seal()
	for range contenders {
		wg.Go(func() {
			if _, ok := r.Resolve(&Callback{Identity: testIdentity(), Data: "feature:opaque"}); !ok {
				t.Error("concurrent resolution lost owner")
			}
		})
	}

	wg.Wait()
}

func TestRegistryOnlyResolvesGroupInteractions(t *testing.T) {
	r := NewRegistry()
	h := &recordingHandler{}
	if err := r.RegisterGroupMessages(h); err != nil {
		t.Fatal(err)
	}

	r.Seal()
	for _, tt := range []struct {
		kind runtimecontract.ChatKind
		chat runtimecontract.ChatID
		want bool
	}{
		{runtimecontract.ChatKindGroup, -1, true},
		{runtimecontract.ChatKindSupergroup, -1, true},
		{"private", 1, false}, {"channel", -1, false}, {"", -1, false},
		{runtimecontract.ChatKindGroup, 1, false}, {runtimecontract.ChatKindGroup, 0, false},
	} {
		identity := testIdentity()
		identity.ChatKind, identity.Chat = tt.kind, tt.chat
		if _, ok := r.Resolve(GroupMessage{Identity: identity}); ok != tt.want {
			t.Errorf("kind=%q chat=%d: resolved=%v", tt.kind, tt.chat, ok)
		}
	}
}

func TestEventsPreserveTypedIdentitiesAndOpaqueFeatureContent(t *testing.T) {
	identity := testIdentity()
	command := Command{Identity: identity, Name: "feature", MessageID: MessageID(13), Arguments: "🧙 unknown feature data"}
	callback := Callback{Identity: identity, QueryID: CallbackQueryID("opaque-query"), Data: "feature:🧙", MessageID: MessageID(13), EphemeralMessageID: EphemeralMessageID(9)}
	message := GroupMessage{Identity: identity, MessageID: MessageID(14), EphemeralMessageID: EphemeralMessageID(10), Text: "🧙 ordinary content"}
	for _, event := range []Event{command, &command, callback, &callback, message, &message} {
		if event.UpdateID() != identity.Update || event.ChatID() != identity.Chat || event.UserID() != identity.User {
			t.Fatalf("identity lost in %T", event)
		}
	}

	r := NewRegistry()
	h := &recordingHandler{}
	if err := r.RegisterCallback("feature:", h); err != nil {
		t.Fatal(err)
	}

	r.Seal()
	resolved, ok := r.Resolve(callback)

	if !ok {
		t.Fatal("callback did not resolve")
	}

	if _, err := resolved.Handle(context.Background(), nil, callback); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(h.event, callback) {
		t.Fatal("opaque event changed")
	}
}
