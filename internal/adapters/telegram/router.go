// Package telegram translates Telegram updates into registered application calls.
package telegram

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/go-telegram/bot/models"
)

type Transport interface {
	Send(context.Context, core.GroupID, core.View) (int, error)
	Edit(context.Context, core.GroupID, int, core.View) error
	Ack(context.Context, string, string) error
}

type Input struct {
	Args       string
	User       core.User
	Recipient  *core.User
	MessageID  int
	CallbackID string
	Data       string
}

type Route func(context.Context, core.Scope, Input) (core.View, error)

type Router struct {
	botID        int64
	username     string
	transport    Transport
	log          *slog.Logger
	commands     map[string]Route
	callbacks    map[string]Route
	bind         Route
	bank         func(context.Context, *models.Update) bool
	orderedMenus bool
}

func NewRouter(botID int64, username string, transport Transport, logger *slog.Logger) (*Router, error) {
	if botID <= 0 || username == "" || transport == nil || logger == nil {
		return nil, errors.New("telegram: identity, transport and logger required")
	}
	return &Router{
		botID:     botID,
		username:  username,
		transport: transport,
		log:       logger,
		commands:  map[string]Route{},
		callbacks: map[string]Route{},
	}, nil
}

func (r *Router) Command(name string, route Route) error {
	if name == "" || route == nil || r.commands[name] != nil {
		return errors.New("duplicate or invalid command")
	}

	r.commands[name] = route
	return nil
}

func (r *Router) Callback(name string, route Route) error {
	if name == "" || route == nil || r.callbacks[name] != nil {
		return errors.New("duplicate or invalid callback")
	}

	r.callbacks[name] = route
	return nil
}

func (r *Router) OnMenuSent(route Route) error {
	if r.bind != nil || route == nil {
		return errors.New("duplicate or invalid menu binding route")
	}

	r.bind = route
	return nil
}

// OrderedMenus delegates every bound-menu edit to the application coordinator.
func (r *Router) OrderedMenus() {
	r.orderedMenus = true
}

func (r *Router) OnBankReply(handler func(context.Context, *models.Update) bool) error {
	if r.bank != nil || handler == nil {
		return errors.New("duplicate or invalid bank reply route")
	}
	r.bank = handler
	return nil
}

func user(u *models.User) *core.User {
	if u == nil || u.ID <= 0 || u.IsBot {
		return nil
	}
	return &core.User{
		ID:        core.UserID(u.ID),
		Username:  u.Username,
		FirstName: u.FirstName,
		LastName:  u.LastName,
	}
}

func group(m *models.Message) bool {
	return m != nil && m.Chat.ID < 0 && (m.Chat.Type == models.ChatTypeGroup || m.Chat.Type == models.ChatTypeSupergroup)
}

func (r *Router) Handle(ctx context.Context, update *models.Update) {
	if update == nil || ctx.Err() != nil {
		return
	}
	if r.bank != nil && r.bank(ctx, update) {
		return
	}

	scope := core.Scope{
		BotID:    r.botID,
		UpdateID: update.ID,
	}
	var input Input
	var route Route
	callback := update.CallbackQuery
	if callback != nil {
		m := callback.Message.Message
		if callback.Message.Type != models.MaybeInaccessibleMessageTypeMessage || !group(m) || m.Date == 0 {
			return
		}

		actor := user(&callback.From)
		if actor == nil {
			return
		}

		scope.Group = core.GroupID(m.Chat.ID)
		scope.Actor = actor.ID
		input = Input{
			User:       *actor,
			MessageID:  m.ID,
			CallbackID: callback.ID,
			Data:       callback.Data,
		}
		namespace, _, _ := strings.Cut(callback.Data, ":")
		route = r.callbacks[namespace]
	} else {
		m := update.Message
		if !group(m) || m.EditDate != 0 || m.SenderChat != nil {
			return
		}

		actor := user(m.From)
		if actor == nil {
			return
		}

		fields := strings.Fields(m.Text)
		if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
			return
		}

		command, address, hasAddress := strings.Cut(strings.TrimPrefix(fields[0], "/"), "@")
		if hasAddress && !strings.EqualFold(address, r.username) {
			return
		}

		route = r.commands[command]
		if route == nil {
			return
		}

		scope.Group = core.GroupID(m.Chat.ID)
		scope.Actor = actor.ID
		input = Input{
			Args:      strings.TrimSpace(strings.TrimPrefix(m.Text, fields[0])),
			User:      *actor,
			MessageID: m.ID,
		}
		if reply := m.ReplyToMessage; reply != nil && reply.SenderChat == nil && reply.Chat.ID == m.Chat.ID {
			input.Recipient = user(reply.From)
		}
	}

	if route == nil {
		if callback != nil {
			_ = r.transport.Ack(ctx, callback.ID, "")
		}
		return
	}

	view, err := route(ctx, scope, input)
	if err != nil {
		r.log.Error("interaction failed", "group", scope.Group, "update", scope.UpdateID)
		if callback != nil {
			_ = r.transport.Ack(ctx, callback.ID, "")
			return
		}
		return
	}

	if callback != nil {
		if len(view.Text) > 0 && !r.orderedMenus {
			if err = r.transport.Edit(ctx, scope.Group, input.MessageID, view); err != nil {
				r.log.Error("menu edit failed", "group", scope.Group)
			}
		}

		if err = r.transport.Ack(ctx, callback.ID, view.Feedback); err != nil {
			r.log.Error("callback acknowledgement failed", "group", scope.Group)
		}
	} else if len(view.Text) > 0 {
		initial := view
		if view.Menu != "" {
			initial.Rows = nil
		}

		var messageID int
		if messageID, err = r.transport.Send(ctx, scope.Group, initial); err != nil {
			r.log.Error("response delivery failed", "group", scope.Group, "update", scope.UpdateID)
		} else if view.Menu != "" && r.bind != nil {
			bound, bindErr := r.bind(ctx, scope, Input{
				Data:      view.Menu,
				MessageID: messageID,
			})
			if bindErr == nil && !r.orderedMenus && (len(bound.Rows) > 0 || !slices.Equal(initial.Text, bound.Text)) {
				bindErr = r.transport.Edit(ctx, scope.Group, messageID, bound)
			}

			if bindErr != nil {
				r.log.Error("menu binding failed", "group", scope.Group)
			}
		}
	}
}

func entities(spans core.Text) (string, []models.MessageEntity, error) {
	var text strings.Builder
	var result []models.MessageEntity
	offset := 0

	for _, span := range spans {
		length := len(utf16.Encode([]rune(span.Text)))
		if length > 0 && span.Style != core.Plain {
			if span.Style != core.Bold && span.Style != core.Code && span.Style != core.Italic {
				return "", nil, errors.New("invalid text style")
			}

			result = append(result, models.MessageEntity{
				Type:   models.MessageEntityType(span.Style),
				Offset: offset,
				Length: length,
			})
		}

		text.WriteString(span.Text)
		offset += length
	}

	if offset == 0 || offset > 4096 {
		return "", nil, errors.New("rendered text exceeds Telegram limits")
	}
	return text.String(), result, nil
}

func keyboard(rows [][]core.Button) (*models.InlineKeyboardMarkup, error) {
	result := &models.InlineKeyboardMarkup{InlineKeyboard: make([][]models.InlineKeyboardButton, 0, len(rows))}

	for _, row := range rows {
		var buttons []models.InlineKeyboardButton

		for _, button := range row {
			if len(button.Data) == 0 || len(button.Data) > 64 {
				return nil, errors.New("invalid callback payload length")
			}

			switch button.Style {
			case core.ButtonDefault, core.ButtonPrimary, core.ButtonSuccess, core.ButtonDanger:
			default:
				return nil, errors.New("invalid button style")
			}

			buttons = append(buttons, models.InlineKeyboardButton{
				Text:         button.Caption,
				CallbackData: button.Data,
				Style:        string(button.Style),
			})
		}

		result.InlineKeyboard = append(result.InlineKeyboard, buttons)
	}
	return result, nil
}
