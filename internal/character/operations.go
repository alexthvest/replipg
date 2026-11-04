package character

import (
	"context"
	"errors"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

// checkOperation distinguishes a matching replay from changed-input identity
// reuse before the caller applies any character transitions.
func checkOperation(ctx context.Context, tx runtimecontract.DBTX, change contract.HealthChange) (revision int64, found bool, err error) {
	if err := change.Validate(); err != nil {
		return 0, false, err
	}
	if tx == nil {
		return 0, false, &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "transaction"}
	}
	rows, queryErr := tx.QueryContext(ctx, `SELECT character_id,kind,amount,applied_revision FROM character_operations WHERE operation_id=?`, change.OperationID)
	if queryErr != nil {
		return 0, false, contract.NewStorageError(queryErr)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			revision, found = 0, false
			err = contract.NewStorageError(errors.Join(err, closeErr))
		}
	}()
	if !rows.Next() {
		if rowErr := rows.Err(); rowErr != nil {
			return 0, false, contract.NewStorageError(rowErr)
		}
		return 0, false, nil
	}
	var characterID contract.ID
	var kind contract.HealthKind
	var amount int64
	if scanErr := rows.Scan(&characterID, &kind, &amount, &revision); scanErr != nil {
		return 0, false, contract.NewStorageError(scanErr)
	}
	if rows.Next() {
		return 0, false, contract.NewStorageError(errors.New("character operation identity invariant failed"))
	}
	if rowErr := rows.Err(); rowErr != nil {
		return 0, false, contract.NewStorageError(rowErr)
	}
	if characterID != change.CharacterID || kind != change.Kind || amount != change.Amount {
		return 0, false, &contract.DomainError{Kind: contract.ErrorOperationConflict}
	}
	return revision, true, nil
}

// recordOperation records accepted mutations, including no-ops, in the caller's
// transaction. Receipts are permanent: removing one could reapply an old action.
func recordOperation(ctx context.Context, tx runtimecontract.DBTX, change contract.HealthChange, appliedRevision int64) error {
	if err := change.Validate(); err != nil {
		return err
	}
	if tx == nil {
		return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "transaction"}
	}
	if appliedRevision < 1 {
		return &contract.DomainError{Kind: contract.ErrorInvalidInput, Field: "applied_revision"}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO character_operations(operation_id,character_id,kind,amount,applied_revision) VALUES(?,?,?,?,?)`, change.OperationID, change.CharacterID, change.Kind, change.Amount, appliedRevision)
	if err != nil {
		return contract.NewStorageError(err)
	}
	return nil
}
