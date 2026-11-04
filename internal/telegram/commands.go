package telegram

import (
	"context"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// SetGroupCommands replaces the default-language menu for all group chats.
func (c *Client) SetGroupCommands(ctx context.Context, declarations []runtimecontract.CommandDeclaration) error {
	if runtimecontract.ValidateCommandDeclarations(declarations) != nil {
		return &APIError{Kind: ErrorValidation}
	}

	commands := make([]models.BotCommand, len(declarations))
	for i, declaration := range declarations {
		commands[i] = models.BotCommand{Command: declaration.Name, Description: declaration.Description, IsEphemeral: declaration.Ephemeral}
	}
	return c.call(ctx, true, func(ctx context.Context) error {
		ok, err := c.bot.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: commands, Scope: &models.BotCommandScopeAllGroupChats{}})
		if err != nil {
			return err
		}

		if !ok {
			return &APIError{Kind: ErrorIndeterminate}
		}

		return nil
	})
}
