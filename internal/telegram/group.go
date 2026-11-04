package telegram

import (
	"context"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// VerifyGroupMember performs fresh bounded lookups, requiring bot administrator rights.
func (c *Client) VerifyGroupMember(ctx context.Context, chat runtimecontract.ChatID, user runtimecontract.UserID) (runtimecontract.MembershipResult, error) {
	result := runtimecontract.MembershipResult{Status: runtimecontract.Unknown}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if chat >= 0 || user <= 0 {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, min(c.timeout, 5*time.Second))
	defer cancel()
	lookup := func(id int64) (*models.ChatMember, error) {
		var member *models.ChatMember
		err := c.call(ctx, false, func(ctx context.Context) error {
			var err error
			member, err = c.bot.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: int64(chat), UserID: id})
			return err
		})
		return member, err
	}
	own, err := lookup(c.botID)
	result.CheckedAt = time.Now().UTC()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, nil
	}
	ownUser, ownStatus := membership(own)
	if ownUser == nil || ownUser.ID != c.botID || !ownUser.IsBot || (ownStatus != runtimecontract.Administrator && ownStatus != runtimecontract.Owner) {
		return result, nil
	}
	actor, err := lookup(int64(user))
	result.CheckedAt = time.Now().UTC()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if err != nil {
		return result, nil
	}
	actorUser, status := membership(actor)
	if actorUser != nil && actorUser.ID == int64(user) && !actorUser.IsBot {
		result.Status = status
	}
	return result, nil
}
func membership(member *models.ChatMember) (*models.User, runtimecontract.MembershipStatus) {
	if member == nil {
		return nil, runtimecontract.Unknown
	}
	switch member.Type {
	case models.ChatMemberTypeOwner:
		if member.Owner != nil {
			return member.Owner.User, runtimecontract.Owner
		}
	case models.ChatMemberTypeAdministrator:
		if member.Administrator != nil {
			return &member.Administrator.User, runtimecontract.Administrator
		}
	case models.ChatMemberTypeMember:
		if member.Member != nil {
			return member.Member.User, runtimecontract.Member
		}
	case models.ChatMemberTypeRestricted:
		if member.Restricted != nil && member.Restricted.IsMember {
			return member.Restricted.User, runtimecontract.Member
		}
	}
	return nil, runtimecontract.Unknown
}

// ReplaceGroupMenu owns only the chat's default, English and Russian scopes.
func (c *Client) ReplaceGroupMenu(ctx context.Context, chat runtimecontract.ChatID, declarations []runtimecontract.CommandDeclaration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if chat >= 0 || runtimecontract.ValidateCommandDeclarations(declarations) != nil {
		return &runtimecontract.MenuError{Kind: runtimecontract.Invalid}
	}
	commands := make([]models.BotCommand, len(declarations))
	for i, d := range declarations {
		commands[i] = models.BotCommand{Command: d.Name, Description: d.Description, IsEphemeral: d.Ephemeral}
	}
	scope := &models.BotCommandScopeChat{ChatID: int64(chat)}
	err := c.call(ctx, true, func(ctx context.Context) error {
		ok, err := c.bot.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: commands, Scope: scope})
		if err != nil {
			return err
		}
		if !ok {
			return &APIError{Kind: ErrorIndeterminate}
		}
		return nil
	})
	for _, language := range []string{"en", "ru"} {
		if err != nil {
			break
		}
		err = c.call(ctx, true, func(ctx context.Context) error {
			ok, err := c.bot.DeleteMyCommands(ctx, &bot.DeleteMyCommandsParams{Scope: scope, LanguageCode: language})
			if err != nil {
				return err
			}
			if !ok {
				return &APIError{Kind: ErrorIndeterminate}
			}
			return nil
		})
	}
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	safe := &runtimecontract.MenuError{Kind: runtimecontract.Transient}
	var api *APIError
	if errors.As(err, &api) {
		safe.RetryAfter = api.RetryAfter
		switch api.Kind {
		case ErrorValidation:
			safe.Kind = runtimecontract.Invalid
		case ErrorAuthorization:
			safe.Kind = runtimecontract.Forbidden
		case ErrorRateLimit:
			safe.Kind = runtimecontract.RateLimited
		case ErrorIndeterminate:
			safe.Kind = runtimecontract.Indeterminate
		}
	}
	return safe
}
