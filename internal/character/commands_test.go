package character

import (
	"context"
	"errors"
	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"strings"
	"testing"
	"time"
)

type commandScope struct{ tx runtimecontract.DBTX }

func (s commandScope) DBTX() runtimecontract.DBTX { return s.tx }
func (commandScope) EnqueueDelivery(context.Context, runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error) {
	panic("unexpected durable response")
}
func (commandScope) Schedule(context.Context, runtimecontract.JobRequest) (runtimecontract.JobID, error) {
	panic("unexpected scheduled work")
}

type fakeInspector struct {
	calls   int
	user    runtimecontract.UserID
	created bool
	err     error
}

func (s *fakeInspector) Ensure(_ context.Context, _ runtimecontract.DBTX, user runtimecontract.UserID) (contract.Snapshot, bool, error) {
	s.calls++
	s.user = user
	return contract.Snapshot{Health: 100, MaxHealth: 100, Attack: 10, Defense: 5, State: contract.StateActive}, s.created, s.err
}

var commandNow = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

func authorizedCommand() interaction.Command {
	return interaction.Command{Identity: interaction.Identity{Update: 1, Chat: -100, User: 7, ChatKind: runtimecontract.ChatKindSupergroup}, Name: "character", EphemeralMessageID: 23, SentAt: commandNow}
}
func TestCharacterCommandAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*interaction.Command)
		want   bool
	}{
		{"valid", func(*interaction.Command) {}, true},
		{"ordinary", func(c *interaction.Command) { c.EphemeralMessageID = 0; c.MessageID = 1 }, false},
		{"missing time", func(c *interaction.Command) { c.SentAt = time.Time{} }, false},
		{"exact expiry", func(c *interaction.Command) { c.SentAt = commandNow.Add(-15 * time.Second) }, false},
		{"expired", func(c *interaction.Command) { c.SentAt = commandNow.Add(-time.Hour) }, false},
		{"bot", func(c *interaction.Command) { c.ActorIsBot = true }, false},
		{"chat sender", func(c *interaction.Command) { c.SenderIsChat = true }, false},
		{"missing actor", func(c *interaction.Command) { c.User = 0 }, false},
		{"private", func(c *interaction.Command) { c.Chat = 7; c.ChatKind = "private" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeInspector{created: true}
			h := commandHandler{service: service, now: func() time.Time { return commandNow }}
			cmd := authorizedCommand()
			tc.change(&cmd)
			result, err := h.Handle(context.Background(), commandScope{}, cmd)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.want {
				if service.calls != 0 || len(result.ImmediateResponses) != 0 {
					t.Fatal("unauthorized effects")
				}
				return
			}
			if service.calls != 1 || service.user != 7 || len(result.ImmediateResponses) != 1 {
				t.Fatal("missing actor operation")
			}
			response, ok := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
			if !ok || response.TargetUserID != 7 || response.Destination.ChatID != -100 {
				t.Fatal("unrestricted response")
			}
			auth, ok := response.Authorization.(runtimecontract.IncomingEphemeralAuthorization)
			if !ok || auth.EphemeralMessageID != 23 || !auth.ValidUntil.Equal(cmd.SentAt.Add(15*time.Second)) {
				t.Fatal("incorrect authorization")
			}
			if err := runtimecontract.ValidateResponseAt(response, commandNow); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCharacterUsageAndFailures(t *testing.T) {
	service := &fakeInspector{}
	h := commandHandler{service: service, now: func() time.Time { return commandNow }}
	cmd := authorizedCommand()
	cmd.Arguments = "other-private-user"
	result, err := h.Handle(context.Background(), commandScope{}, cmd)
	if err != nil || service.calls != 0 || len(result.ImmediateResponses) != 1 {
		t.Fatal("usage")
	}
	response := result.ImmediateResponses[0].(runtimecontract.EphemeralResponse)
	if strings.Contains(response.Content.Text, cmd.Arguments) {
		t.Fatal("leaked argument")
	}
	cmd.Arguments = ""
	service.err = contract.NewStorageError(errors.New("private SQL"))
	result, err = h.Handle(context.Background(), commandScope{}, cmd)
	if !errors.Is(err, contract.ErrStorage) || len(result.ImmediateResponses) != 0 {
		t.Fatal("swallowed storage failure")
	}
}
