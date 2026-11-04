package inventory

import (
	"context"
	"math"
	"sort"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// Grant atomically adds caller-resolved rewards to an existing character.
// It preserves pending starter entitlement and equipment. Results are tentative
// until the caller commits its transaction; matching operations are replay-safe.
func (s *Service) Grant(ctx context.Context, tx runtimecontract.DBTX, req contract.GrantRequest) (contract.MutationResult, error) {
	var result contract.MutationResult

	err := withInventoryOperation(ctx, tx, func() error {
		var err error

		result, err = s.grant(ctx, tx, req, "grant")
		return err
	})
	if err != nil {
		return contract.MutationResult{}, err
	}

	return result, nil
}

// grant runs inside the public operation's savepoint; it never creates a character.
func (s *Service) grant(ctx context.Context, tx runtimecontract.DBTX, req contract.GrantRequest, kind string) (contract.MutationResult, error) {
	canonical, err := canonicalGrant(req)
	if err != nil {
		return contract.MutationResult{}, err
	}

	record := operationRecord{OperationID: req.OperationID, CharacterID: req.CharacterID, Kind: kind, CanonicalRequest: canonical}
	stored, replay, err := checkOperation(ctx, tx, record)
	if err != nil {
		return contract.MutationResult{}, err
	}

	if replay {
		current, _, err := readInventory(ctx, tx, s.catalog, req.CharacterID)
		return contract.MutationResult{AppliedRevision: stored.AppliedRevision, Current: current}, err
	}

	// canonicalGrant validated every line and checked aggregate arithmetic.
	// Apply exactly those canonical quantities once per item.
	totals := make(map[itemcontract.ItemID]int64, len(req.Items))
	for _, line := range req.Items {
		totals[line.ItemID] += line.Quantity
	}

	lines := make([]contract.GrantLine, 0, len(totals))
	for id, quantity := range totals {
		if _, ok := s.catalog.Lookup(id); !ok {
			return contract.MutationResult{}, contract.ErrNotFound
		}

		lines = append(lines, contract.GrantLine{ItemID: id, Quantity: quantity})
	}

	sort.Slice(lines, func(i, j int) bool { return lines[i].ItemID < lines[j].ItemID })
	if _, err := characterOwner(ctx, tx, req.CharacterID); err != nil {
		return contract.MutationResult{}, err
	}

	if err := ensureInventoryRoot(ctx, tx, req.CharacterID); err != nil {
		return contract.MutationResult{}, err
	}

	current, _, err := readInventoryMetadata(ctx, tx, s.catalog, req.CharacterID)
	if err != nil {
		return contract.MutationResult{}, err
	}

	if current.Revision == math.MaxInt64 {
		return contract.MutationResult{}, contract.ErrExhausted
	}

	// Validate the complete reward against ownership before writing any line.
	for _, line := range lines {
		owned, err := ownedQuantity(ctx, tx, req.CharacterID, line.ItemID)
		if err != nil {
			return contract.MutationResult{}, err
		}

		if owned > math.MaxInt64-line.Quantity {
			return contract.MutationResult{}, contract.ErrExhausted
		}
	}

	for _, line := range lines {
		if err := increaseQuantity(ctx, tx, req.CharacterID, line.ItemID, line.Quantity); err != nil {
			return contract.MutationResult{}, err
		}
	}

	if err := advanceInventoryRevision(ctx, tx, req.CharacterID, current.Revision); err != nil {
		return contract.MutationResult{}, err
	}

	record.AppliedRevision = current.Revision + 1
	if err := recordOperation(ctx, tx, record); err != nil {
		return contract.MutationResult{}, err
	}

	current, _, err = readInventory(ctx, tx, s.catalog, req.CharacterID)
	return contract.MutationResult{Applied: true, AppliedRevision: record.AppliedRevision, Current: current}, err
}
