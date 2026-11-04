package inventory

import (
	"context"
	"errors"
	"strings"
	"time"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Register installs the production personal inventory command and opaque controls.
func Register(registry *interaction.Registry, service *Service, now func() time.Time) error {
	if registry == nil || service == nil || now == nil {
		return contract.ErrInvalidInput
	}
	handler := inventoryHandler{service, now}
	if err := registry.RegisterCommand("inventory", handler); err != nil {
		return err
	}
	return registry.RegisterCallback("inv:", handler)
}

type inventoryHandler struct {
	service *Service
	now     func() time.Time
}

func (h inventoryHandler) Handle(ctx context.Context, scope runtimecontract.WorkScope, event interaction.Event) (interaction.HandlerResult, error) {
	now := h.now()
	switch e := event.(type) {
	case *interaction.Command:
		if e != nil {
			return h.Handle(ctx, scope, *e)
		}
	case *interaction.Callback:
		if e != nil {
			return h.Handle(ctx, scope, *e)
		}
	case interaction.Command:
		if e.Name != "inventory" || !authorizedInventoryCommand(e, now) {
			return interaction.HandlerResult{}, nil
		}
		auth := runtimecontract.IncomingEphemeralAuthorization{EphemeralMessageID: int(e.EphemeralMessageID), ValidUntil: e.SentAt.Add(15 * time.Second)}
		if strings.TrimSpace(e.Arguments) != "" {
			return inventoryResponse(e.Identity, auth, runtimecontract.MessageContent{Text: "Usage: /inventory"})
		}
		result, err := h.service.Open(ctx, scope.DBTX(), e.User)
		if err != nil {
			return interaction.HandlerResult{}, err
		}
		content, err := h.service.pageContent(ctx, scope.DBTX(), now, e.Identity, result.Current.CharacterID, 0)
		if err != nil {
			return interaction.HandlerResult{}, err
		}
		return inventoryResponse(e.Identity, auth, content)
	case interaction.Callback:
		if e.Update <= 0 || e.User <= 0 || e.Chat >= 0 || e.QueryID == "" || e.EphemeralMessageID <= 0 || int64(int(e.EphemeralMessageID)) != int64(e.EphemeralMessageID) || !inventoryGroup(e.ChatKind) {
			return interaction.HandlerResult{}, nil
		}
		control, err := resolveControl(ctx, scope.DBTX(), now, e.User, e.Chat, e.Data)
		var storage runtimecontract.StorageFailure
		if errors.As(err, &storage) {
			return interaction.HandlerResult{}, err
		}
		deadline := now.Add(15 * time.Second)
		if err == nil && control.ExpiresAt.Before(deadline) {
			deadline = control.ExpiresAt
		}
		auth := runtimecontract.CallbackAuthorization{CallbackQueryID: string(e.QueryID), OriginatedFromEphemeral: e.EphemeralMessageID > 0, ValidUntil: deadline}
		if errors.Is(err, contract.ErrNotFound) {
			return interaction.HandlerResult{}, nil
		}
		if errors.Is(err, contract.ErrExpiredControl) {
			return inventoryResponseForMessage(e.Identity, auth, int(e.EphemeralMessageID), runtimecontract.MessageContent{Text: "This inventory control expired. Open /inventory again."})
		}
		if err != nil {
			return interaction.HandlerResult{}, err
		}
		var content runtimecontract.MessageContent
		switch control.Action {
		case "page":
			content, err = h.service.pageContent(ctx, scope.DBTX(), now, e.Identity, control.CharacterID, control.Page)
		case "detail":
			content, err = h.service.detailContent(ctx, scope.DBTX(), now, e.Identity, control)
		case "equip", "unequip":
			item := control.ItemID
			if control.Action == "unequip" {
				item = ""
			}
			var result contract.MutationResult
			result, err = h.service.ChangeEquipment(ctx, scope.DBTX(), contract.EquipmentChange{OperationID: contract.OperationID("inventory:control:" + control.Token), CharacterID: control.CharacterID, Slot: control.Slot, ItemID: item, ExpectedRevision: control.ExpectedRevision})
			if err == nil {
				control.ExpectedRevision = result.Current.Revision
				content, err = h.service.detailContent(ctx, scope.DBTX(), now, e.Identity, control)
			}
		default:
			return interaction.HandlerResult{}, nil
		}
		// Cleanup failures can wrap an expected rejection; storage classification
		// takes precedence so the dispatcher rolls back and retries the update.
		if errors.As(err, &storage) {
			return interaction.HandlerResult{}, err
		}
		if errors.Is(err, contract.ErrNotOwned) || errors.Is(err, contract.ErrStaleControl) {
			return inventoryResponseForMessage(e.Identity, auth, int(e.EphemeralMessageID), runtimecontract.MessageContent{Text: "Inventory changed. Open /inventory again."})
		}
		if err != nil {
			return interaction.HandlerResult{}, err
		}
		return inventoryResponseForMessage(e.Identity, auth, int(e.EphemeralMessageID), content)
	}
	return interaction.HandlerResult{}, nil
}
func inventoryGroup(kind runtimecontract.ChatKind) bool {
	return kind == runtimecontract.ChatKindGroup || kind == runtimecontract.ChatKindSupergroup
}
func authorizedInventoryCommand(command interaction.Command, now time.Time) bool {
	return command.Update > 0 && command.User > 0 && command.Chat < 0 && inventoryGroup(command.ChatKind) && !command.ActorIsBot && !command.SenderIsChat && command.EphemeralMessageID > 0 && int64(int(command.EphemeralMessageID)) == int64(command.EphemeralMessageID) && !command.SentAt.IsZero() && now.Before(command.SentAt.Add(15*time.Second))
}
func inventoryResponse(identity interaction.Identity, auth runtimecontract.EphemeralAuthorization, content runtimecontract.MessageContent) (interaction.HandlerResult, error) {
	return inventoryResponseForMessage(identity, auth, 0, content)
}
func inventoryResponseForMessage(identity interaction.Identity, auth runtimecontract.EphemeralAuthorization, editMessageID int, content runtimecontract.MessageContent) (interaction.HandlerResult, error) {
	response := runtimecontract.EphemeralResponse{Destination: runtimecontract.GroupDestination{ChatID: identity.Chat, Kind: identity.ChatKind}, TargetUserID: identity.User, Authorization: auth, Content: content, EditMessageID: editMessageID}
	if err := runtimecontract.ValidateResponse(response); err != nil {
		return interaction.HandlerResult{}, err
	}
	return interaction.HandlerResult{ImmediateResponses: []runtimecontract.ResponseRequest{response}}, nil
}
func (s *Service) pageContent(ctx context.Context, tx runtimecontract.DBTX, now time.Time, identity interaction.Identity, id charactercontract.ID, page int) (runtimecontract.MessageContent, error) {
	view, err := readInventoryPage(ctx, tx, s.catalog, id, page)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}
	base := inventoryControl{UserID: identity.User, ChatID: identity.Chat, CharacterID: id, Page: view.Page, ExpectedRevision: view.Current.Revision}
	controls := viewControls{}
	for _, item := range view.Current.Items {
		control := base
		control.Action = "detail"
		control.ItemID = item.ItemID
		data, err := createControl(ctx, tx, now, control)
		if err != nil {
			return runtimecontract.MessageContent{}, err
		}
		controls.Items = append(controls.Items, data)
	}
	if view.Page > 0 {
		control := base
		control.Action = "page"
		control.Page--
		controls.Previous, err = createControl(ctx, tx, now, control)
		if err != nil {
			return runtimecontract.MessageContent{}, err
		}
	}
	if view.HasNext {
		control := base
		control.Action = "page"
		control.Page++
		controls.Next, err = createControl(ctx, tx, now, control)
		if err != nil {
			return runtimecontract.MessageContent{}, err
		}
	}
	return renderPage(s.catalog, view, identity.ActorUsername, controls), nil
}
func (s *Service) detailContent(ctx context.Context, tx runtimecontract.DBTX, now time.Time, identity interaction.Identity, control inventoryControl) (runtimecontract.MessageContent, error) {
	snapshot, _, err := readInventoryMetadata(ctx, tx, s.catalog, control.CharacterID)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}
	quantity, err := ownedQuantity(ctx, tx, control.CharacterID, control.ItemID)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}
	snapshot.Items = []contract.OwnedItem{{ItemID: control.ItemID, Quantity: quantity}}
	back := control
	back.Action = "page"
	back.ItemID = ""
	back.Slot = ""
	back.ExpectedRevision = snapshot.Revision
	data, err := createControl(ctx, tx, now, back)
	if err != nil {
		return runtimecontract.MessageContent{}, err
	}
	controls := viewControls{Back: data}
	item, found := s.catalog.Lookup(control.ItemID)
	if found && (item.Category == contract.CategoryWeapon || item.Category == contract.CategoryArmor) {
		action := control
		action.ExpectedRevision = snapshot.Revision
		action.Slot = contract.Slot(item.Category)
		action.Action = "equip"
		if snapshot.Weapon == item.ID || snapshot.Armor == item.ID {
			action.Action = "unequip"
		}
		token, err := createControl(ctx, tx, now, action)
		if err != nil {
			return runtimecontract.MessageContent{}, err
		}
		if action.Action == "equip" {
			controls.Equip = token
		} else {
			controls.Unequip = token
		}
	}
	return renderDetail(s.catalog, snapshot, control.ItemID, identity.ActorUsername, controls)
}
