// Package interaction defines transport-neutral feature events and route ownership.
package interaction

import (
	"context"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// CommandName is a feature-owned canonical command without transport syntax.
type CommandName string

// CallbackPrefix is a feature-owned prefix of opaque callback data.
type CallbackPrefix string

// MessageID identifies an ordinary message within its chat.
type MessageID int64

// CallbackQueryID identifies a callback for acknowledgement and authorization.
type CallbackQueryID string

// EphemeralMessageID identifies an ephemeral message separately from ordinary messages.
type EphemeralMessageID int64

// Identity carries the originating update, group and actor without transport models.
type Identity struct {
	Update   runtimecontract.UpdateID
	Chat     runtimecontract.ChatID
	User     runtimecontract.UserID
	ChatKind runtimecontract.ChatKind
	// ActorUsername is optional transient presentation metadata. It must never
	// determine ownership or authorization, or enter persistent feature state.
	ActorUsername string
}

func (i Identity) UpdateID() runtimecontract.UpdateID { return i.Update }
func (i Identity) ChatID() runtimecontract.ChatID     { return i.Chat }
func (i Identity) UserID() runtimecontract.UserID     { return i.User }

// EventKind identifies an interaction variant without gameplay meaning.
type EventKind string

const (
	EventKindCommand      EventKind = "command"
	EventKindCallback     EventKind = "callback"
	EventKindGroupMessage EventKind = "group_message"
)

// Event is a closed set of interaction values supplied by the transport mapper.
type Event interface {
	UpdateID() runtimecontract.UpdateID
	ChatID() runtimecontract.ChatID
	UserID() runtimecontract.UserID
	eventKind() EventKind
}

// Command contains a canonical name; argument interpretation belongs to its feature.
type Command struct {
	Identity
	Name               CommandName
	MessageID          MessageID
	EphemeralMessageID EphemeralMessageID
	Arguments          string
	SentAt             time.Time
	ActorIsBot         bool
	SenderIsChat       bool
}

func (Command) eventKind() EventKind { return EventKindCommand }

// Callback preserves opaque feature data and the original callback identity.
type Callback struct {
	Identity
	QueryID            CallbackQueryID
	MessageID          MessageID
	EphemeralMessageID EphemeralMessageID
	Data               string
}

func (Callback) eventKind() EventKind { return EventKindCallback }

// GroupMessage exposes ordinary message content without assigning domain meaning.
type GroupMessage struct {
	Identity
	MessageID          MessageID
	EphemeralMessageID EphemeralMessageID
	Text               string
}

func (GroupMessage) eventKind() EventKind { return EventKindGroupMessage }

// Handler is feature behavior invoked by transactional dispatch, never by resolution.
type Handler interface {
	Handle(ctx context.Context, scope runtimecontract.WorkScope, event Event) (HandlerResult, error)
}

// HandlerResult carries feature-requested immediate responses for later validation.
type HandlerResult struct {
	ImmediateResponses []runtimecontract.ResponseRequest
}
