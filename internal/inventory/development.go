package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alexthvest/replipg/internal/localization"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// RegisterDevelopment installs explicitly enabled self-only verification probes.
func RegisterDevelopment(registry *interaction.Registry, service *Service, now func() time.Time, p Presentation) error {
	if err := p.validate(); err != nil {
		return err
	}
	if registry == nil || service == nil || now == nil {
		return contract.ErrInvalidInput
	}
	h := developmentHandler{service: service, now: now, presentation: p}
	if err := registry.RegisterCommand("dev_inventory_grant", h); err != nil {
		return err
	}

	return registry.RegisterCommand("dev_inventory_heal", h)
}

type developmentHandler struct {
	service      *Service
	now          func() time.Time
	presentation Presentation
}

func (h developmentHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	command, ok := event.(interaction.Command)
	if pointer, isPointer := event.(*interaction.Command); isPointer && pointer != nil {
		command = *pointer
		ok = true
	}
	if !ok || !authorizedInventoryCommand(command, h.now()) || (command.Name != "dev_inventory_grant" && command.Name != "dev_inventory_heal") {
		return interaction.HandlerResult{}, nil
	}

	locale, err := h.presentation.Resolve(ctx, scope.DBTX(), command.Chat)
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	auth := runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: int(command.EphemeralMessageID), ValidUntil: command.SentAt.Add(15 * time.Second)}
	respond := func(key localization.Key) (interaction.HandlerResult, error) {
		text, err := h.presentation.Catalog.Text(locale, key, nil)
		if err != nil {
			return interaction.HandlerResult{}, err
		}

		return inventoryResponse(command.Identity, auth, runtimecontract.MessageContent{Text: text})
	}
	fields := strings.Fields(command.Arguments)
	grant := command.Name == "dev_inventory_grant"
	usage := localization.Key("usage.heal")
	count := 1
	if grant {
		usage = "usage.grant"
		count = 2
	}
	if len(fields) != count {
		return respond(usage)
	}
	item := contract.ItemID(fields[0])
	if item.Validate() != nil {
		return respond(usage)
	}

	quantity := int64(1)
	if grant {
		for _, r := range fields[1] {
			if r < '0' || r > '9' {
				return respond(usage)
			}
		}

		var err error

		quantity, err = strconv.ParseInt(fields[1], 10, 64)
		if err != nil || quantity <= 0 {
			return respond(usage)
		}
	}
	var id charactercontract.ID

	err = queryInventoryRows(ctx, scope.DBTX(), `SELECT id,user_id FROM characters WHERE user_id=?`, []any{command.User}, func(rows *sql.Rows) error {
		var owner runtimecontract.UserID

		if err := rows.Scan(&id, &owner); err != nil {
			return err
		}

		if owner != command.User {
			return errCharacterIdentity
		}

		return nil
	})
	if err != nil {
		return interaction.HandlerResult{}, err
	}

	if id == "" {
		return respond("inventory.prepare")
	}

	operation := contract.OperationID(fmt.Sprintf("inventory:dev:%s:%d:%d", command.Name, command.User, command.Update))
	if grant {
		_, err = h.service.Grant(ctx, scope.DBTX(), contract.GrantRequest{OperationID: operation, CharacterID: id, Items: []contract.GrantLine{{ItemID: item, Quantity: quantity}}})
	} else {
		_, err = h.service.ConsumeHealing(ctx, scope.DBTX(), contract.ConsumeRequest{OperationID: operation, CharacterID: id, ItemID: item})
	}
	if err != nil {
		var storage runtimecontract.StorageFailure

		if errors.As(err, &storage) {
			return interaction.HandlerResult{}, err
		}

		switch {
		case errors.Is(err, contract.ErrOperationConflict):
			return respond("inventory.conflict")
		case errors.Is(err, contract.ErrFullHealth):
			return respond("inventory.full")
		case errors.Is(err, contract.ErrIncapacitated):
			return respond("inventory.incapacitated")
		case errors.Is(err, contract.ErrNotOwned):
			return respond("inventory.not_owned")
		case errors.Is(err, contract.ErrNotUsable):
			return respond("inventory.not_usable")
		case errors.Is(err, contract.ErrNotFound), errors.Is(err, contract.ErrInvalidInput):
			return respond("inventory.unknown")
		case errors.Is(err, contract.ErrExhausted):
			return respond("inventory.exhausted")
		default:
			return interaction.HandlerResult{}, err
		}
	}
	if grant {
		return respond("inventory.granted")
	}

	return respond("inventory.healed")
}
