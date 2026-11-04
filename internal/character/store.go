package character

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

type characterRecord struct {
	snapshot       contract.Snapshot
	recoveryHealth int64
}

func updateCharacter(ctx context.Context, tx runtimecontract.DBTX, snapshot contract.Snapshot, recoveryHealth, previousRevision int64) error {
	var deadline, recovery any

	if snapshot.State == contract.StateIncapacitated {
		deadline, recovery = snapshot.RecoverAt.UnixMilli(), recoveryHealth
	}
	result, err := tx.ExecContext(ctx, `UPDATE characters SET health=?,recover_at_ms=?,recovery_health=?,revision=? WHERE id=? AND revision=?`, snapshot.Health, deadline, recovery, snapshot.Revision, snapshot.ID, previousRevision)
	if err != nil {
		return contract.NewStorageError(err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return contract.NewStorageError(err)
	}

	if affected != 1 {
		return contract.NewStorageError(errors.New("character revision invariant failed"))
	}

	return nil
}

func insertCharacter(ctx context.Context, tx runtimecontract.DBTX, id contract.ID, userID runtimecontract.UserID, cfg Config) (bool, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO characters(id,user_id,max_health,health,attack,defense,revision) VALUES(?,?,?,?,?,?,1) ON CONFLICT(user_id) DO NOTHING`, id, userID, cfg.maxHealth, cfg.maxHealth, cfg.attack, cfg.defense)
	if err != nil {
		return false, contract.NewStorageError(err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return false, contract.NewStorageError(err)
	}

	return affected == 1, nil
}

func readCharacterByUser(ctx context.Context, tx runtimecontract.DBTX, userID runtimecontract.UserID) (record characterRecord, found bool, err error) {
	return readCharacter(ctx, tx, `SELECT id,user_id,max_health,health,attack,defense,recover_at_ms,recovery_health,revision FROM characters WHERE user_id=?`, userID)
}

func readCharacterByID(ctx context.Context, tx runtimecontract.DBTX, id contract.ID) (characterRecord, bool, error) {
	return readCharacter(ctx, tx, `SELECT id,user_id,max_health,health,attack,defense,recover_at_ms,recovery_health,revision FROM characters WHERE id=?`, id)
}

func readCharacter(ctx context.Context, tx runtimecontract.DBTX, query string, identity any) (record characterRecord, found bool, err error) {
	rows, queryErr := tx.QueryContext(ctx, query, identity)
	if queryErr != nil {
		return characterRecord{}, false, contract.NewStorageError(queryErr)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			record, found = characterRecord{}, false
			err = contract.NewStorageError(errors.Join(err, closeErr))
		}
	}()

	if !rows.Next() {
		if rowErr := rows.Err(); rowErr != nil {
			return characterRecord{}, false, contract.NewStorageError(rowErr)
		}

		return characterRecord{}, false, nil
	}

	var recoverAt, recoveryHealth sql.NullInt64

	snapshot := &record.snapshot
	if scanErr := rows.Scan(&snapshot.ID, &snapshot.UserID, &snapshot.MaxHealth, &snapshot.Health, &snapshot.Attack, &snapshot.Defense, &recoverAt, &recoveryHealth, &snapshot.Revision); scanErr != nil {
		return characterRecord{}, false, contract.NewStorageError(scanErr)
	}

	snapshot.State = contract.StateActive
	if recoverAt.Valid {
		snapshot.State = contract.StateIncapacitated
		snapshot.RecoverAt = time.UnixMilli(recoverAt.Int64).UTC()
		record.recoveryHealth = recoveryHealth.Int64
	}
	if rows.Next() {
		return characterRecord{}, false, contract.NewStorageError(errors.New("character ownership invariant failed"))
	}

	if rowErr := rows.Err(); rowErr != nil {
		return characterRecord{}, false, contract.NewStorageError(rowErr)
	}

	return record, true, nil
}
