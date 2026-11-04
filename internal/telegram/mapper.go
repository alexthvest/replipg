package telegram

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// DecodeStatus describes transport validation before durable dispatch.
type DecodeStatus string

const (
	DecodeMapped  DecodeStatus = "mapped"
	DecodeIgnored DecodeStatus = "ignored"
	DecodeFailed  DecodeStatus = "decode_failed"
)

// ErrUpdateID means no safe durable completion identity can be established.
var ErrUpdateID = errors.New("telegram: invalid update identity")

// DecodedUpdate separates dispatchable events from terminal transport outcomes.
type DecodedUpdate struct {
	UpdateID        runtimecontract.UpdateID
	Event           interaction.Event
	Status          DecodeStatus
	CallbackQueryID interaction.CallbackQueryID
}

// DecodeUpdate decodes identity before content and never exposes payload errors.
// Errors have no usable identity; known-ID failures must be durably recorded.
func DecodeUpdate(raw json.RawMessage) (DecodedUpdate, error) {
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return DecodedUpdate{}, ErrUpdateID
	}
	var id int64
	if json.Unmarshal(envelope["update_id"], &id) != nil || id <= 0 {
		return DecodedUpdate{}, ErrUpdateID
	}
	result := DecodedUpdate{UpdateID: runtimecontract.UpdateID(id), Status: DecodeIgnored}
	messageRaw, messagePresent := envelope["message"]
	callbackRaw, callbackPresent := envelope["callback_query"]
	if messagePresent && callbackPresent {
		result.Status = DecodeFailed
		return result, nil
	}
	if callbackPresent {
		var callback struct {
			ID      string          `json:"id"`
			From    *inboundUser    `json:"from"`
			Message *inboundMessage `json:"message"`
			Data    *string         `json:"data"`
		}
		if json.Unmarshal(callbackRaw, &callback) != nil {
			result.Status = DecodeFailed
			return result, nil
		}
		result.CallbackQueryID = interaction.CallbackQueryID(callback.ID)
		if callback.ID == "" || callback.From == nil || callback.From.ID <= 0 {
			result.Status = DecodeFailed
			return result, nil
		}
		if callback.Message == nil || callback.Data == nil {
			return result, nil
		}
		message := callback.Message
		if message.Chat.Type == "" {
			result.Status = DecodeFailed
			return result, nil
		}
		if !message.group() || (message.Date != nil && *message.Date == 0) {
			return result, nil
		}
		if !message.valid() {
			result.Status = DecodeFailed
			return result, nil
		}
		identity := message.identity(result.UpdateID, callback.From.ID)
		identity.ActorUsername = callback.From.Username
		result.Event = interaction.Callback{Identity: identity, ActorIsBot: callback.From.IsBot, QueryID: result.CallbackQueryID, MessageID: interaction.MessageID(message.MessageID), EphemeralMessageID: interaction.EphemeralMessageID(message.EphemeralMessageID), Data: *callback.Data, Source: message.callbackSource()}
	} else if messagePresent {
		var message *inboundMessage
		if json.Unmarshal(messageRaw, &message) != nil || message == nil {
			result.Status = DecodeFailed
			return result, nil
		}
		if message.Chat.Type == "" {
			result.Status = DecodeFailed
			return result, nil
		}
		if !message.group() {
			return result, nil
		}
		if !message.valid() || message.From == nil || message.From.ID <= 0 {
			result.Status = DecodeFailed
			return result, nil
		}
		identity := message.identity(result.UpdateID, message.From.ID)
		name, args, command, valid := message.command()
		if !valid {
			result.Status = DecodeFailed
			return result, nil
		}
		if command {
			identity.ActorUsername = message.From.Username
			result.Event = interaction.Command{Identity: identity, MessageID: interaction.MessageID(message.MessageID), EphemeralMessageID: interaction.EphemeralMessageID(message.EphemeralMessageID), Name: interaction.CommandName(name), Arguments: args, SentAt: message.sentAt(), ActorIsBot: message.From.IsBot, SenderIsChat: len(message.SenderChat) > 0 && string(message.SenderChat) != "null"}
		} else {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(messageRaw, &fields)
			content := false
			for _, key := range []string{"text", "photo", "video", "animation", "audio", "document", "sticker", "voice", "video_note", "contact", "location", "venue", "poll", "dice", "game", "story", "paid_media"} {
				if _, ok := fields[key]; ok {
					content = true
					break
				}
			}
			if !content {
				return result, nil
			}
			result.Event = interaction.GroupMessage{Identity: identity, MessageID: interaction.MessageID(message.MessageID), EphemeralMessageID: interaction.EphemeralMessageID(message.EphemeralMessageID), Text: message.Text}
		}
	}
	if result.Event != nil {
		result.Status = DecodeMapped
	}
	return result, nil
}

type inboundUser struct {
	ID       int64  `json:"id"`
	IsBot    bool   `json:"is_bot"`
	Username string `json:"username"`
}
type inboundMessage struct {
	MessageID          int64           `json:"message_id"`
	EphemeralMessageID int64           `json:"ephemeral_message_id"`
	From               *inboundUser    `json:"from"`
	Date               *int64          `json:"date"`
	SenderChat         json.RawMessage `json:"sender_chat"`
	Chat               struct {
		ID   int64                    `json:"id"`
		Type runtimecontract.ChatKind `json:"type"`
	} `json:"chat"`
	Text         string          `json:"text"`
	Entities     json.RawMessage `json:"entities"`
	ReceiverUser json.RawMessage `json:"receiver_user"`
	ReplyMarkup  json.RawMessage `json:"reply_markup"`
}

type inboundEntity struct {
	Type   string `json:"type"`
	Offset *int   `json:"offset"`
	Length *int   `json:"length"`
	URL    string `json:"url"`
}

func (m inboundMessage) sentAt() time.Time {
	if m.Date == nil || *m.Date <= 0 {
		return time.Time{}
	}
	return time.Unix(*m.Date, 0).UTC()
}

func (m inboundMessage) group() bool {
	return m.Chat.Type == runtimecontract.ChatKindGroup || m.Chat.Type == runtimecontract.ChatKindSupergroup
}
func (m inboundMessage) valid() bool {
	return m.Chat.ID < 0 && m.MessageID >= 0 && m.EphemeralMessageID >= 0 && (m.MessageID > 0 || m.EphemeralMessageID > 0)
}
func (m inboundMessage) identity(update runtimecontract.UpdateID, user int64) interaction.Identity {
	return interaction.Identity{Update: update, Chat: runtimecontract.ChatID(m.Chat.ID), User: runtimecontract.UserID(user), ChatKind: m.Chat.Type}
}
func (m inboundMessage) command() (name, args string, command, valid bool) {
	var entities []struct {
		Type   string `json:"type"`
		Offset int    `json:"offset"`
		Length int    `json:"length"`
	}
	if len(m.Entities) > 0 && json.Unmarshal(m.Entities, &entities) != nil {
		return "", "", false, false
	}
	for _, entity := range entities {
		if entity.Type != "bot_command" || entity.Offset != 0 {
			continue
		}
		units := utf16.Encode([]rune(m.Text))
		if entity.Length <= 1 || entity.Length > len(units) {
			return "", "", false, false
		}
		token := string(utf16.Decode(units[:entity.Length]))
		if !strings.HasPrefix(token, "/") {
			return "", "", false, false
		}
		token = strings.TrimPrefix(token, "/")
		var target string
		var targeted bool
		name, target, targeted = strings.Cut(token, "@")
		if targeted {
			if target == "" {
				return "", "", false, false
			}
			for _, char := range target {
				if !commandCharacter(char) {
					return "", "", false, false
				}
			}
		}
		if len(name) == 0 || len(name) > 32 {
			return "", "", false, false
		}
		for _, char := range name {
			if !commandCharacter(char) {
				return "", "", false, false
			}
		}
		rest := string(utf16.Decode(units[entity.Length:]))
		if rest != "" {
			first := []rune(rest)[0]
			if !unicode.IsSpace(first) {
				return "", "", false, false
			}
		}
		return strings.ToLower(name), strings.TrimLeftFunc(rest, unicode.IsSpace), true, true
	}
	return "", "", false, true
}

func commandCharacter(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_'
}

func (m inboundMessage) callbackSource() *interaction.CallbackSource {
	if m.EphemeralMessageID <= 0 || m.Text == "" || !utf8.ValidString(m.Text) {
		return nil
	}
	source := &interaction.CallbackSource{
		Body:               runtimecontract.MessageContent{Text: m.Text},
		EphemeralMessageID: m.EphemeralMessageID,
	}
	var entities []inboundEntity
	if len(m.Entities) > 0 && json.Unmarshal(m.Entities, &entities) != nil {
		return nil
	}
	// Check every raw entity before excluding automatic lexical annotations.
	boundaries := map[int]bool{0: true}
	textLength := 0
	for _, r := range m.Text {
		textLength += utf16.RuneLen(r)
		boundaries[textLength] = true
	}
	for _, entity := range entities {
		if entity.Offset == nil || entity.Length == nil {
			return nil
		}
		offset, length := *entity.Offset, *entity.Length
		if offset < 0 || length <= 0 || length > textLength || offset > textLength-length || !boundaries[offset] || !boundaries[offset+length] {
			return nil
		}
		switch entity.Type {
		case "mention", "hashtag", "cashtag", "bot_command", "url", "email", "phone_number":
			if entity.URL != "" {
				return nil
			}
			continue
		case "bold", "italic", "code", "text_link":
			if (entity.Type == "text_link") != (entity.URL != "") || !utf8.ValidString(entity.URL) {
				return nil
			}
		default:
			return nil
		}
		source.Body.Entities = append(source.Body.Entities, runtimecontract.TextEntity{
			Kind: runtimecontract.TextEntityKind(entity.Type), Offset: offset, Length: length, URL: entity.URL,
		})
	}
	if len(m.ReceiverUser) > 0 && string(m.ReceiverUser) != "null" {
		var receiver inboundUser
		if json.Unmarshal(m.ReceiverUser, &receiver) != nil || receiver.ID <= 0 {
			return nil
		}
		source.ReceiverUserID = runtimecontract.UserID(receiver.ID)
	}
	if len(m.ReplyMarkup) > 0 && string(m.ReplyMarkup) != "null" {
		var markup struct {
			InlineKeyboard [][]struct {
				CallbackData *string `json:"callback_data"`
			} `json:"inline_keyboard"`
		}
		if json.Unmarshal(m.ReplyMarkup, &markup) != nil {
			return nil
		}
		for _, row := range markup.InlineKeyboard {
			for _, button := range row {
				if button.CallbackData != nil {
					if *button.CallbackData == "" || len(*button.CallbackData) > 64 || !utf8.ValidString(*button.CallbackData) {
						return nil
					}
					source.CallbackData = append(source.CallbackData, *button.CallbackData)
				}
			}
		}
	}
	return source
}
