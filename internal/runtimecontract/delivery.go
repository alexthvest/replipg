package runtimecontract

import (
	"context"
	"time"
)

// DeliveryReceipt identifies a definitively accepted Telegram message.
// Ephemeral receipts may have a zero ordinary MessageID.
type DeliveryReceipt struct {
	ChatID             ChatID
	MessageID          int64
	EphemeralMessageID int64
}

// DeliveryOutcomeKind determines whether durable work may be retried.
type DeliveryOutcomeKind string

const (
	DeliveryAccepted         DeliveryOutcomeKind = "accepted"
	DeliveryRetryableFailure DeliveryOutcomeKind = "retryable_failure"
	DeliveryPermanentFailure DeliveryOutcomeKind = "permanent_failure"
	DeliveryIndeterminate    DeliveryOutcomeKind = "indeterminate"
	DeliveryCancelled        DeliveryOutcomeKind = "cancelled"
)

// DeliveryOutcome contains safe evidence from one attempt, never raw API text.
type DeliveryOutcome struct {
	Kind       DeliveryOutcomeKind
	Receipt    *DeliveryReceipt
	RetryAfter time.Duration
	ReasonCode string
}

// Sender delivers responses without exposing transport library models.
type Sender interface {
	SendPublic(context.Context, PublicResponse) (DeliveryReceipt, error)
	SendEphemeral(context.Context, EphemeralResponse) (DeliveryReceipt, error)
	AcknowledgeCallback(context.Context, CallbackAcknowledgement) error
}
