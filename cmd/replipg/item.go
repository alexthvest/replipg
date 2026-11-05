package main

import (
	"context"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/item"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/presentation"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type itemDevelopmentHandler struct {
	owner   runtimecontract.UserID
	items   item.Catalog
	catalog *localization.Catalog
	resolve func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error)
	now     func() time.Time
}

func (h itemDevelopmentHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	command, ok := event.(interaction.Command)
	if pointer, isPointer := event.(*interaction.Command); isPointer && pointer != nil {
		command = *pointer
		ok = true
	}

	if !ok || h.owner <= 0 || command.User != h.owner || command.Name != "dev_item" {
		return interaction.HandlerResult{}, nil
	}

	now := h.now()
	if command.Update <= 0 || command.Chat >= 0 || (command.ChatKind != runtimecontract.ChatKindGroup && command.ChatKind != runtimecontract.ChatKindSupergroup) || command.ActorIsBot || command.SenderIsChat || command.EphemeralMessageID <= 0 || int64(int(command.EphemeralMessageID)) != int64(command.EphemeralMessageID) || command.SentAt.IsZero() || !now.Before(command.SentAt.Add(15*time.Second)) {
		return interaction.HandlerResult{}, nil
	}

	locale, err := h.resolve(ctx, scope.DBTX(), command.Chat)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	text, err := h.text(locale, command.Arguments)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	response := runtimecontract.EphemeralResponse{Destination: runtimecontract.GroupDestination{ChatID: command.Chat, Kind: command.ChatKind}, TargetUserID: command.User, Authorization: runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: int(command.EphemeralMessageID), ValidUntil: command.SentAt.Add(15 * time.Second)}, Content: text}
	if err := runtimecontract.ValidateResponse(response); err != nil {
		return interaction.HandlerResult{}, err
	}

	return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{response}}, nil
}

func (h itemDevelopmentHandler) text(locale localization.Locale, arguments string) (runtimecontract.MessageContent, error) {
	fields := strings.Fields(arguments)
	if len(fields) != 1 || itemcontract.ItemID(fields[0]).Validate() != nil {
		return presentation.Localized(h.catalog, locale, "usage.item", nil)
	}

	definition, ok := h.items.Lookup(itemcontract.ItemID(fields[0]))
	if !ok {
		return presentation.Localized(h.catalog, locale, "item.unknown", nil)
	}

	translated, err := h.catalog.Item(locale, string(definition.ID))
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	category, err := h.catalog.Text(locale, localization.Key("category."+string(definition.Category)), nil)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	text, err := presentation.Localized(h.catalog, locale, "item.probe", localization.Args{"name": localization.String(translated.Name), "id": localization.String(string(definition.ID)), "category": localization.String(category)})
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	var key localization.Key
	var value int64
	switch definition.Category {
	case itemcontract.CategoryWeapon:
		key = "inventory.attack"
		value = definition.AttackBonus
	case itemcontract.CategoryArmor:
		key = "inventory.defense"
		value = definition.DefenseBonus
	case itemcontract.CategoryConsumable:
		key = "inventory.healing"
		value = definition.HealingHP
	default:
		return text, nil
	}

	mechanics, err := presentation.Localized(h.catalog, locale, key, localization.Args{"value": localization.Integer(value)})
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}

	var body presentation.Builder

	body.Append(text)
	body.Write("\n")
	body.Append(mechanics)
	return body.Content(), nil
}
