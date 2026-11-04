package inventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"sort"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type operationRecord struct {
	OperationID      contract.OperationID
	CharacterID      charactercontract.ID
	Kind             string
	CanonicalRequest string
	AppliedRevision  int64
	EffectKind       contract.EffectKind
	HealingHP        int64
}

func canonicalGrant(req contract.GrantRequest) (string, error) {
	if err := req.Validate(); err != nil {
		return "", err
	}
	totals := make(map[contract.ItemID]int64, len(req.Items))
	for _, line := range req.Items {
		if totals[line.ItemID] > math.MaxInt64-line.Quantity {
			return "", &contract.DomainError{Kind: contract.ErrorExhausted, Field: "quantity"}
		}

		totals[line.ItemID] += line.Quantity
	}

	type line struct {
		ItemID   contract.ItemID `json:"item_id"`
		Quantity int64           `json:"quantity"`
	}

	lines := make([]line, 0, len(totals))
	for id, quantity := range totals {
		lines = append(lines, line{id, quantity})
	}

	sort.Slice(lines, func(i, j int) bool { return lines[i].ItemID < lines[j].ItemID })
	return canonicalJSON(struct {
		Version int    `json:"version"`
		Items   []line `json:"items"`
	}{1, lines})
}

func canonicalEquipment(req contract.EquipmentChange) (string, error) {
	if err := req.Validate(); err != nil {
		return "", err
	}

	return canonicalJSON(struct {
		Version          int             `json:"version"`
		Slot             contract.Slot   `json:"slot"`
		ItemID           contract.ItemID `json:"item_id"`
		ExpectedRevision int64           `json:"expected_revision"`
	}{1, req.Slot, req.ItemID, req.ExpectedRevision})
}

func canonicalConsume(req contract.ConsumeRequest, effect contract.EffectDefinition) (string, error) {
	if err := req.Validate(); err != nil {
		return "", err
	}

	if err := effect.Validate(); err != nil {
		return "", err
	}

	if req.ItemID != effect.ItemID {
		return "", &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "effect_item"}
	}

	return canonicalJSON(struct {
		Version    int                 `json:"version"`
		ItemID     contract.ItemID     `json:"item_id"`
		EffectKind contract.EffectKind `json:"effect_kind"`
		HealingHP  int64               `json:"healing_hp"`
	}{1, req.ItemID, effect.Kind, effect.HealingHP})
}

// The fixed structures above contain only validated scalars, so encoding cannot fail.
func canonicalJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", contract.NewStorageError(err)
	}

	return string(encoded), nil
}

func validateOperation(record operationRecord) error {
	if err := record.OperationID.Validate(); err != nil {
		return err
	}

	if err := record.CharacterID.Validate(); err != nil {
		return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "character_id"}
	}

	if record.CanonicalRequest == "" {
		return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "canonical_request"}
	}

	switch record.Kind {
	case "consume":
		if !record.EffectKind.Valid() || record.HealingHP <= 0 {
			return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "effect"}
		}
	case "starter", "grant", "equipment":
		if record.EffectKind != "" || record.HealingHP != 0 {
			return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "effect"}
		}
	default:
		return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "kind"}
	}
	return nil
}

// Receipts are checked before inspecting current quantities, health or revision.
func checkOperation(ctx context.Context, tx runtimecontract.DBTX, want operationRecord) (operationRecord, bool, error) {
	if err := validateOperation(want); err != nil {
		return operationRecord{}, false, err
	}

	if tx == nil {
		return operationRecord{}, false, &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "transaction"}
	}

	stored := operationRecord{OperationID: want.OperationID}
	found := false
	err := queryInventoryRows(ctx, tx, `SELECT character_id,kind,canonical_request,applied_revision,effect_kind,healing_hp FROM inventory_operations WHERE operation_id=?`, []any{want.OperationID}, func(rows *sql.Rows) error {
		if found {
			return errors.New("inventory operation identity invariant failed")
		}

		var kind sql.NullString
		var healing sql.NullInt64

		if err := rows.Scan(&stored.CharacterID, &stored.Kind, &stored.CanonicalRequest, &stored.AppliedRevision, &kind, &healing); err != nil {
			return err
		}
		stored.EffectKind, stored.HealingHP = contract.EffectKind(kind.String), healing.Int64
		found = true
		return nil
	})
	if err != nil {
		return operationRecord{}, false, err
	}

	if !found {
		return operationRecord{}, false, nil
	}

	if stored.CharacterID != want.CharacterID || stored.Kind != want.Kind || stored.CanonicalRequest != want.CanonicalRequest || stored.EffectKind != want.EffectKind || stored.HealingHP != want.HealingHP {
		return operationRecord{}, false, &contract.DomainError{Kind: contract.ErrorOperationConflict}
	}

	return stored, true, nil
}

// Permanent receipts share the caller's transaction and have no expiry path.
func recordOperation(ctx context.Context, tx runtimecontract.DBTX, record operationRecord) error {
	if err := validateOperation(record); err != nil {
		return err
	}

	if tx == nil || record.AppliedRevision < 1 {
		return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "transaction_or_revision"}
	}

	var effect, healing any

	if record.Kind == "consume" {
		effect, healing = record.EffectKind, record.HealingHP
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO inventory_operations(operation_id,character_id,kind,canonical_request,applied_revision,effect_kind,healing_hp) VALUES(?,?,?,?,?,?,?)`, record.OperationID, record.CharacterID, record.Kind, record.CanonicalRequest, record.AppliedRevision, effect, healing)
	if err != nil {
		return contract.NewStorageError(err)
	}

	return nil
}

// Public mutations call private helpers within this single, non-nested savepoint.
// Cleanup failures must reach the outer transaction owner even after rejection.
func withInventoryOperation(ctx context.Context, tx runtimecontract.DBTX, action func() error) error {
	if tx == nil || action == nil {
		return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "transaction_or_action"}
	}

	if _, err := tx.ExecContext(ctx, `SAVEPOINT inventory_operation`); err != nil {
		return contract.NewStorageError(err)
	}

	actionErr := action()
	if actionErr != nil {
		var domain *contract.DomainError
		var characterDomain *charactercontract.DomainError
		var storage runtimecontract.StorageFailure

		if !errors.As(actionErr, &domain) && !errors.As(actionErr, &characterDomain) && !errors.As(actionErr, &storage) && !expectedInventoryFailure(actionErr) {
			actionErr = contract.NewStorageError(actionErr)
		}
	}
	if actionErr != nil {
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT inventory_operation`); err != nil {
			return contract.NewStorageError(errors.Join(actionErr, err))
		}
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT inventory_operation`); err != nil {
		return contract.NewStorageError(errors.Join(actionErr, err))
	}

	return actionErr
}

func expectedInventoryFailure(err error) bool {
	for _, expected := range []error{contract.ErrNotOwned, contract.ErrIncompatibleSlot, contract.ErrStaleControl, contract.ErrExpiredControl, contract.ErrNotUsable, contract.ErrFullHealth, contract.ErrInvalidInput, contract.ErrInvalidCatalog, contract.ErrNotFound, contract.ErrIncapacitated, contract.ErrOperationConflict, contract.ErrExhausted} {
		if errors.Is(err, expected) {
			return true
		}
	}
	return false
}

func starterOperationID(id charactercontract.ID) contract.OperationID {
	return contract.OperationID("inventory:starter:" + string(id))
}

func healingOperationID(id contract.OperationID) charactercontract.OperationID {
	digest := sha256.Sum256([]byte(id))
	return charactercontract.OperationID("inventory:heal:" + hex.EncodeToString(digest[:]))
}
