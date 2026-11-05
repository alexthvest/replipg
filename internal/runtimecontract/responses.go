package runtimecontract

import (
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

const maxMessageTextUTF16 = 4096

// ChatKind is the supported Telegram group destination category without
// exposing transport models.
type ChatKind string

const (
	ChatKindGroup      ChatKind = "group"
	ChatKindSupergroup ChatKind = "supergroup"
)

// GroupDestination prevents response contracts from representing a private
// chat fallback.
type GroupDestination struct {
	ChatID ChatID
	Kind   ChatKind
}

func (d GroupDestination) validate() error {
	if d.ChatID == 0 {
		return validationError("destination.chat_id", ReasonRequired)
	}

	if d.ChatID > 0 {
		return validationError("destination.chat_id", ReasonOutOfRange)
	}

	if d.Kind != ChatKindGroup && d.Kind != ChatKindSupergroup {
		return validationError("destination.kind", ReasonUnsupported)
	}

	return nil
}

// TextEntityKind identifies supported domain-neutral text formatting.
type TextEntityKind string

const (
	TextEntityBold     TextEntityKind = "bold"
	TextEntityItalic   TextEntityKind = "italic"
	TextEntityCode     TextEntityKind = "code"
	TextEntityTextLink TextEntityKind = "text_link"
)

// TextEntity uses Telegram-compatible UTF-16 offsets while remaining
// transport-neutral.
type TextEntity struct {
	Kind   TextEntityKind
	Offset int
	Length int
	URL    string
}

// InlineButton is a callback button owned by domain-neutral response content.
type InlineButton struct {
	Text         string
	CallbackData string
	Style        ButtonStyle `json:",omitempty"`
}

// ButtonStyle is an optional semantic color; the empty value uses client defaults.
type ButtonStyle string

const (
	ButtonPrimary ButtonStyle = "primary"
	ButtonSuccess ButtonStyle = "success"
	ButtonDanger  ButtonStyle = "danger"
)

// MessageContent is supported text content with optional formatting and
// callback-button rows.
type MessageContent struct {
	Text     string
	Entities []TextEntity
	Buttons  [][]InlineButton
}

func (c MessageContent) validate() error {
	if c.Text == "" {
		return validationError("content.text", ReasonRequired)
	}

	if !utf8.ValidString(c.Text) {
		return validationError("content.text", ReasonInvalid)
	}

	textLength := len(utf16.Encode([]rune(c.Text)))
	boundaries := utf16Boundaries(c.Text, textLength)
	if textLength > maxMessageTextUTF16 {
		return validationError("content.text", ReasonOutOfRange)
	}

	for _, entity := range c.Entities {
		if entity.Kind != TextEntityBold && entity.Kind != TextEntityItalic && entity.Kind != TextEntityCode && entity.Kind != TextEntityTextLink {
			return validationError("content.entities.kind", ReasonUnsupported)
		}

		if entity.Offset < 0 || entity.Length <= 0 || entity.Offset > textLength-entity.Length {
			return validationError("content.entities.range", ReasonOutOfRange)
		}

		if !boundaries[entity.Offset] || !boundaries[entity.Offset+entity.Length] {
			return validationError("content.entities.range", ReasonInvalid)
		}

		if entity.Kind == TextEntityTextLink && entity.URL == "" {
			return validationError("content.entities.url", ReasonRequired)
		}

		if entity.Kind != TextEntityTextLink && entity.URL != "" {
			return validationError("content.entities.url", ReasonInconsistent)
		}
	}

	for _, row := range c.Buttons {
		if len(row) == 0 {
			return validationError("content.buttons.row", ReasonRequired)
		}

		for _, button := range row {
			if button.Style != "" && button.Style != ButtonPrimary && button.Style != ButtonSuccess && button.Style != ButtonDanger {
				return validationError("content.buttons.style", ReasonUnsupported)
			}

			if button.Text == "" {
				return validationError("content.buttons.text", ReasonRequired)
			}

			if !utf8.ValidString(button.Text) {
				return validationError("content.buttons.text", ReasonInvalid)
			}

			if button.CallbackData == "" {
				return validationError("content.buttons.callback_data", ReasonRequired)
			}

			if !utf8.ValidString(button.CallbackData) {
				return validationError("content.buttons.callback_data", ReasonInvalid)
			}

			if len([]byte(button.CallbackData)) > 64 {
				return validationError("content.buttons.callback_data", ReasonOutOfRange)
			}
		}
	}
	return nil
}

func utf16Boundaries(text string, textLength int) []bool {
	boundaries := make([]bool, textLength+1)
	boundaries[0] = true
	offset := 0
	for _, value := range text {
		offset += utf16.RuneLen(value)
		boundaries[offset] = true
	}
	return boundaries
}

// ResponseRequest is a transport-neutral immediate response. The closed set
// prevents callers from inventing visibility modes or fallback destinations.
type ResponseRequest interface {
	responseRequest()
}

// ValidateResponse rejects invalid response shapes, including typed-nil
// interface values, without performing transport or other I/O.
func ValidateResponse(request ResponseRequest) error {
	return validateResponseAt(request, nil)
}

// ValidateResponseAt additionally checks short-lived authorization evidence
// against the supplied instant. Supplying time makes expiry deterministic.
func ValidateResponseAt(request ResponseRequest, now time.Time) error {
	return validateResponseAt(request, &now)
}

func validateResponseAt(request ResponseRequest, now *time.Time) error {
	switch value := request.(type) {
	case PublicResponse:
		return value.validate()
	case *PublicResponse:
		if value == nil {
			return validationError("response", ReasonRequired)
		}

		return value.validate()
	case EphemeralResponse:
		return value.validate(now)
	case *EphemeralResponse:
		if value == nil {
			return validationError("response", ReasonRequired)
		}

		return value.validate(now)
	case CallbackAcknowledgement:
		return value.validate()
	case *CallbackAcknowledgement:
		if value == nil {
			return validationError("response", ReasonRequired)
		}

		return value.validate()
	default:
		return validationError("response", ReasonRequired)
	}
}

// PublicResponse requests a group-visible response.
type PublicResponse struct {
	Destination GroupDestination
	Content     MessageContent
}

func (PublicResponse) responseRequest() {}

func (r PublicResponse) validate() error {
	if err := r.Destination.validate(); err != nil {
		return err
	}

	return r.Content.validate()
}

// EphemeralAuthorization is the closed set of evidence that permits a
// user-specific response in a group.
type EphemeralAuthorization interface {
	validate(replaceCallbackMessage bool) error
	ephemeralAuthorization()
}

// CallbackAuthorization authorizes a response from a callback interaction.
type CallbackAuthorization struct {
	CallbackQueryID         string
	OriginatedFromEphemeral bool
	ValidUntil              time.Time
}

func (CallbackAuthorization) ephemeralAuthorization() {}

func (a CallbackAuthorization) validate(replace bool) error {
	if a.CallbackQueryID == "" {
		return validationError("authorization.callback_query_id", ReasonRequired)
	}

	if a.ValidUntil.IsZero() {
		return validationError("authorization.valid_until", ReasonRequired)
	}

	if replace && a.OriginatedFromEphemeral {
		return validationError("authorization.replace_callback_message", ReasonInconsistent)
	}

	return nil
}

// IncomingEphemeralAuthorization authorizes a reply to an incoming ephemeral
// message.
type IncomingEphemeralAuthorization struct {
	EphemeralMessageID int
	ValidUntil         time.Time
}

func (IncomingEphemeralAuthorization) ephemeralAuthorization() {}

func (a IncomingEphemeralAuthorization) validate(replace bool) error {
	if a.EphemeralMessageID <= 0 {
		return validationError("authorization.ephemeral_message_id", ReasonOutOfRange)
	}

	if a.ValidUntil.IsZero() {
		return validationError("authorization.valid_until", ReasonRequired)
	}

	if replace {
		return validationError("authorization.replace_callback_message", ReasonInconsistent)
	}

	return nil
}

// AdministratorAuthorization records that administrator privileges, rather
// than short-lived interaction context, authorize the response.
type AdministratorAuthorization struct{}

func (AdministratorAuthorization) ephemeralAuthorization() {}

func (AdministratorAuthorization) validate(replace bool) error {
	if replace {
		return validationError("authorization.replace_callback_message", ReasonInconsistent)
	}

	return nil
}

// EphemeralResponse requests visibility restricted to one non-bot user in a
// group and carries the evidence required for that operation.
type EphemeralResponse struct {
	Destination            GroupDestination
	TargetUserID           UserID
	TargetIsBot            bool
	Authorization          EphemeralAuthorization
	ReplaceCallbackMessage bool
	// EditMessageID selects an existing ephemeral message; zero sends a new one.
	EditMessageID int
	Content       MessageContent
}

func (EphemeralResponse) responseRequest() {}

func (r EphemeralResponse) validate(now *time.Time) error {
	if err := r.Destination.validate(); err != nil {
		return err
	}

	if r.TargetUserID == 0 {
		return validationError("target.user_id", ReasonRequired)
	}

	if r.TargetUserID < 0 {
		return validationError("target.user_id", ReasonOutOfRange)
	}

	if r.TargetIsBot {
		return validationError("target.user_id", ReasonInvalid)
	}

	if r.Authorization == nil {
		return validationError("authorization", ReasonRequired)
	}

	validUntil, err := validateEphemeralAuthorization(r.Authorization, r.ReplaceCallbackMessage)
	if err != nil {
		return err
	}

	if now != nil && !validUntil.IsZero() && !now.Before(validUntil) {
		return validationError("authorization.valid_until", ReasonOutOfRange)
	}

	if r.EditMessageID < 0 {
		return validationError("edit_message_id", ReasonOutOfRange)
	}

	if r.EditMessageID > 0 {
		var callback CallbackAuthorization

		switch auth := r.Authorization.(type) {
		case CallbackAuthorization:
			callback = auth
		case *CallbackAuthorization:
			callback = *auth
		default:
			return validationError("edit_message_id", ReasonInconsistent)
		}

		if !callback.OriginatedFromEphemeral || r.ReplaceCallbackMessage {
			return validationError("edit_message_id", ReasonInconsistent)
		}
	}
	return r.Content.validate()
}

func validateEphemeralAuthorization(authorization EphemeralAuthorization, replace bool) (time.Time, error) {
	switch value := authorization.(type) {
	case CallbackAuthorization:
		return value.ValidUntil, value.validate(replace)
	case *CallbackAuthorization:
		if value == nil {
			return time.Time{}, validationError("authorization", ReasonRequired)
		}

		return value.ValidUntil, value.validate(replace)
	case IncomingEphemeralAuthorization:
		return value.ValidUntil, value.validate(replace)
	case *IncomingEphemeralAuthorization:
		if value == nil {
			return time.Time{}, validationError("authorization", ReasonRequired)
		}

		return value.ValidUntil, value.validate(replace)
	case AdministratorAuthorization:
		return time.Time{}, value.validate(replace)
	case *AdministratorAuthorization:
		if value == nil {
			return time.Time{}, validationError("authorization", ReasonRequired)
		}

		return time.Time{}, value.validate(replace)
	default:
		return time.Time{}, validationError("authorization", ReasonUnsupported)
	}
}

// CallbackAcknowledgement requests an invisible callback acknowledgement.
type CallbackAcknowledgement struct {
	CallbackQueryID string
}

func (CallbackAcknowledgement) responseRequest() {}

func (r CallbackAcknowledgement) validate() error {
	if r.CallbackQueryID == "" {
		return validationError("callback_query_id", ReasonRequired)
	}

	return nil
}
