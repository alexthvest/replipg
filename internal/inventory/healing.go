package inventory

import (
	"context"
	"database/sql"
	"errors"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Effect exposes a trusted fixed healing definition to gameplay consumers.
func (s *Service) Effect(id contract.ItemID) (contract.EffectDefinition, error) {
	if err := id.Validate(); err != nil {
		return contract.EffectDefinition{}, err
	}
	item, ok := s.catalog.Lookup(id)
	if !ok {
		return contract.EffectDefinition{}, &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "item_id"}
	}
	if item.Category != contract.CategoryConsumable {
		return contract.EffectDefinition{}, &contract.DomainError{Kind: contract.ErrorNotUsable}
	}
	return contract.EffectDefinition{ItemID: id, Kind: contract.EffectHealSelfFixedHP, HealingHP: item.HealingHP}, nil
}

// ConsumeHealing atomically spends one item and heals through the character service.
// The caller supplies gameplay authorization and owns the final transaction commit.
func (s *Service) ConsumeHealing(ctx context.Context, tx runtimecontract.DBTX, req contract.ConsumeRequest) (contract.ConsumeResult, error) {
	if err := req.Validate(); err != nil {
		return contract.ConsumeResult{}, err
	}

	var result contract.ConsumeResult

	err := withInventoryOperation(ctx, tx, func() error {
		effect, err := s.consumptionEffect(ctx, tx, req)
		if err != nil {
			return err
		}

		canonical, err := canonicalConsume(req, effect)
		if err != nil {
			return err
		}

		record := operationRecord{OperationID: req.OperationID, CharacterID: req.CharacterID, Kind: "consume", CanonicalRequest: canonical, EffectKind: effect.Kind, HealingHP: effect.HealingHP}
		stored, replay, err := checkOperation(ctx, tx, record)
		if err != nil {
			return err
		}

		current, _, err := readInventory(ctx, tx, s.catalog, req.CharacterID)
		if err != nil {
			return err
		}

		if !replay {
			quantity, err := ownedQuantity(ctx, tx, req.CharacterID, req.ItemID)
			if err != nil {
				return err
			}

			if quantity < 1 {
				return &contract.DomainError{Kind: contract.ErrorNotOwned}
			}
		}
		owner, err := characterOwner(ctx, tx, req.CharacterID)
		if err != nil {
			return err
		}

		ch, found, err := s.characters.Lookup(ctx, tx, owner)
		if err != nil {
			return err
		}

		if !found || ch.ID != req.CharacterID || ch.UserID != owner {
			return contract.NewStorageError(errCharacterIdentity)
		}

		if replay {
			result = contract.ConsumeResult{Mutation: contract.MutationResult{AppliedRevision: stored.AppliedRevision, Current: current}, Character: ch, HealingHP: stored.HealingHP}
			return nil
		}

		if ch.State == charactercontract.StateIncapacitated {
			return &contract.DomainError{Kind: contract.ErrorIncapacitated}
		}

		if ch.Health >= ch.MaxHealth {
			return &contract.DomainError{Kind: contract.ErrorFullHealth}
		}

		healed, err := s.characters.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: healingOperationID(req.OperationID), CharacterID: req.CharacterID, Kind: charactercontract.HealthHeal, Amount: effect.HealingHP})
		if err != nil {
			return err
		}

		if !healed.Applied {
			return contract.NewStorageError(errors.New("character healing receipt without inventory receipt"))
		}

		if healed.Current.ID != req.CharacterID || healed.Current.UserID != owner {
			return contract.NewStorageError(errCharacterIdentity)
		}

		if err := decreaseQuantity(ctx, tx, req.CharacterID, req.ItemID, 1); err != nil {
			return err
		}

		if err := advanceInventoryRevision(ctx, tx, req.CharacterID, current.Revision); err != nil {
			return err
		}

		record.AppliedRevision = current.Revision + 1
		if err := recordOperation(ctx, tx, record); err != nil {
			return err
		}

		current, _, err = readInventory(ctx, tx, s.catalog, req.CharacterID)
		if err != nil {
			return err
		}

		result = contract.ConsumeResult{Mutation: contract.MutationResult{Applied: true, AppliedRevision: record.AppliedRevision, Current: current}, Character: healed.Current, HealingHP: effect.HealingHP}
		return nil
	})
	if err != nil {
		return contract.ConsumeResult{}, err
	}

	return result, nil
}

// An existing identity is compared using its frozen effect before consulting
// current definitions, so changed item inputs conflict even when not usable.
func (s *Service) consumptionEffect(ctx context.Context, tx runtimecontract.DBTX, req contract.ConsumeRequest) (contract.EffectDefinition, error) {
	var target charactercontract.ID
	var kind string
	var effectKind sql.NullString
	var healing sql.NullInt64

	found := false
	err := queryInventoryRows(ctx, tx, `SELECT character_id,kind,effect_kind,healing_hp FROM inventory_operations WHERE operation_id=?`, []any{req.OperationID}, func(rows *sql.Rows) error {
		found = true
		return rows.Scan(&target, &kind, &effectKind, &healing)
	})
	if err != nil {
		return contract.EffectDefinition{}, err
	}

	if !found {
		return s.Effect(req.ItemID)
	}

	if target != req.CharacterID || kind != "consume" {
		return contract.EffectDefinition{}, &contract.DomainError{Kind: contract.ErrorOperationConflict}
	}
	effect := contract.EffectDefinition{ItemID: req.ItemID, Kind: contract.EffectKind(effectKind.String), HealingHP: healing.Int64}
	if err := effect.Validate(); err != nil {
		return contract.EffectDefinition{}, contract.NewStorageError(err)
	}

	return effect, nil
}
