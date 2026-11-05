package sharing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const (
	PublicKind     runtimecontract.DeliveryKind = "menu_share_public"
	FeedbackKind   runtimecontract.DeliveryKind = "menu_share_feedback"
	PayloadVersion uint16                       = 1
)

// DeliveryState is the feature's content-free projection of durable delivery state.
type DeliveryState string

const (
	Pending   DeliveryState = "pending"
	Completed DeliveryState = "completed"
	Rejected  DeliveryState = "rejected"
	Unknown   DeliveryState = "unknown"
)

// Evidence records authoritative delivery timestamps without retained content.
type Evidence struct {
	State                 DeliveryState
	UpdatedAt, TerminalAt time.Time
}

// DeliveryLedger confines runtime integration to transaction-bound work operations.
type DeliveryLedger interface {
	Enqueue(context.Context, runtimecontract.DBTX, runtimecontract.DeliveryRequest) (runtimecontract.DeliveryID, error)
	Inspect(context.Context, runtimecontract.DBTX, runtimecontract.DeliveryID) (Evidence, error)
	Purge(context.Context, runtimecontract.DBTX, runtimecontract.DeliveryKind, runtimecontract.DeliveryID, time.Time) (bool, error)
}

// PublicPayload is the immutable v1 group snapshot schema.
type PublicPayload struct {
	ActionUpdateID runtimecontract.UpdateID         `json:"action_update_id"`
	Destination    runtimecontract.GroupDestination `json:"destination"`
	Content        runtimecontract.MessageContent   `json:"content"`
}

// FeedbackPayload keeps callback evidence explicit rather than serializing an interface.
type FeedbackPayload struct {
	ActionUpdateID  runtimecontract.UpdateID         `json:"action_update_id"`
	Destination     runtimecontract.GroupDestination `json:"destination"`
	TargetUserID    runtimecontract.UserID           `json:"target_user_id"`
	CallbackQueryID string                           `json:"callback_query_id"`
	ValidUntilMS    int64                            `json:"valid_until_ms"`
	Content         runtimecontract.MessageContent   `json:"content"`
}

// EncodePublic captures the complete snapshot without personal authorization.
func EncodePublic(p PublicPayload) (runtimecontract.VersionedPayload, error) {
	if _, err := p.response(); err != nil {
		return runtimecontract.VersionedPayload{}, err
	}

	return encodePayload(p)
}

// EncodeFeedback freezes the original callback identity and authorization deadline.
func EncodeFeedback(p FeedbackPayload) (runtimecontract.VersionedPayload, error) {
	if _, err := p.response(); err != nil {
		return runtimecontract.VersionedPayload{}, err
	}

	return encodePayload(p)
}

// DecodePublic validates durable work without resolving locale or reading live state.
func DecodePublic(payload runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) {
	var p PublicPayload

	if err := decodePayload(payload, &p); err != nil {
		return nil, err
	}

	return p.response()
}

// DecodeFeedback reconstructs concrete callback authorization with its fixed expiry.
func DecodeFeedback(payload runtimecontract.VersionedPayload) (runtimecontract.ResponseRequest, error) {
	var p FeedbackPayload

	if err := decodePayload(payload, &p); err != nil {
		return nil, err
	}

	return p.response()
}

func (p PublicPayload) response() (runtimecontract.ResponseRequest, error) {
	if p.ActionUpdateID <= 0 || len(p.Content.Buttons) != 0 {
		return nil, errors.New("sharing: invalid public payload")
	}

	response := runtimecontract.PublicResponse{Destination: p.Destination, Content: p.Content}
	if err := runtimecontract.ValidateResponse(response); err != nil {
		return nil, err
	}

	return response, nil
}

func (p FeedbackPayload) response() (runtimecontract.ResponseRequest, error) {
	if p.ActionUpdateID <= 0 || p.ValidUntilMS <= 0 || len(p.Content.Buttons) != 0 {
		return nil, errors.New("sharing: invalid feedback payload")
	}

	response := runtimecontract.EphemeralResponse{
		Destination: p.Destination, TargetUserID: p.TargetUserID, Content: p.Content,
		Authorization: runtimecontract.CallbackAuthorization{CallbackQueryID: p.CallbackQueryID, OriginatedFromEphemeral: true, ValidUntil: time.UnixMilli(p.ValidUntilMS)},
	}
	if err := runtimecontract.ValidateResponse(response); err != nil {
		return nil, err
	}

	return response, nil
}

func encodePayload(value any) (runtimecontract.VersionedPayload, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return runtimecontract.VersionedPayload{}, errors.New("sharing: invalid delivery payload")
	}

	return runtimecontract.NewVersionedPayload(PayloadVersion, data)
}

func decodePayload(payload runtimecontract.VersionedPayload, target any) error {
	if err := payload.ValidateVersion(PayloadVersion); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(payload.Data()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("sharing: invalid delivery payload")
	}

	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("sharing: trailing delivery payload")
	}

	return nil
}

// NewPublicRequest creates send-once durable work for a frozen public snapshot.
func NewPublicRequest(p PublicPayload) (runtimecontract.DeliveryRequest, error) {
	payload, err := EncodePublic(p)
	if err != nil {
		return runtimecontract.DeliveryRequest{}, err
	}

	return deliveryRequest(PublicKind, payload), nil
}

// NewFeedbackRequest creates send-once work whose failure cannot trigger a public post.
func NewFeedbackRequest(p FeedbackPayload) (runtimecontract.DeliveryRequest, error) {
	payload, err := EncodeFeedback(p)
	if err != nil {
		return runtimecontract.DeliveryRequest{}, err
	}

	return deliveryRequest(FeedbackKind, payload), nil
}

func deliveryRequest(kind runtimecontract.DeliveryKind, payload runtimecontract.VersionedPayload) runtimecontract.DeliveryRequest {
	return runtimecontract.DeliveryRequest{Kind: kind, Payload: payload, Retry: runtimecontract.RetryPolicy{MaxAttempts: 1, InitialDelay: time.Second, MaxDelay: time.Second}}
}
