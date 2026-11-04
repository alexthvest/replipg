package inventory

import (
	"context"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// ChangeEquipment atomically assigns a compatible owned item or clears a slot.
// Matching replay reports the current loadout and original application revision.
func (s *Service) ChangeEquipment(ctx context.Context, tx runtimecontract.DBTX, request contract.EquipmentChange) (contract.MutationResult, error) {
	canonical, err := canonicalEquipment(request)
	if err != nil {
		return contract.MutationResult{}, err
	}
	var result contract.MutationResult
	err = withInventoryOperation(ctx, tx, func() error {
		record := operationRecord{OperationID: request.OperationID, CharacterID: request.CharacterID, Kind: "equipment", CanonicalRequest: canonical}
		stored, replay, err := checkOperation(ctx, tx, record)
		if err != nil {
			return err
		}
		current, _, err := readInventory(ctx, tx, s.catalog, request.CharacterID)
		if err != nil {
			return err
		}
		if replay {
			result = contract.MutationResult{AppliedRevision: stored.AppliedRevision, Current: current}
			return nil
		}
		if current.Revision != request.ExpectedRevision {
			return contract.ErrStaleControl
		}
		changed, err := equipmentRule(current, s.catalog, request)
		if err != nil {
			return err
		}
		if changed {
			if err := writeEquipment(ctx, tx, request.CharacterID, request.Slot, request.ItemID); err != nil {
				return err
			}
			if err := advanceInventoryRevision(ctx, tx, request.CharacterID, current.Revision); err != nil {
				return err
			}
			current.Revision++
		}
		record.AppliedRevision = current.Revision
		if err := recordOperation(ctx, tx, record); err != nil {
			return err
		}
		current, _, err = readInventory(ctx, tx, s.catalog, request.CharacterID)
		if err != nil {
			return err
		}
		result = contract.MutationResult{Applied: true, AppliedRevision: record.AppliedRevision, Current: current}
		return nil
	})
	if err != nil {
		return contract.MutationResult{}, err
	}
	return result, nil
}
