package inventory

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"strings"
	"time"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const controlLifetime = 5 * time.Minute

const controlCleanupBatch = 100

type inventoryControl struct {
	Token            string
	UserID           runtimecontract.UserID
	ChatID           runtimecontract.ChatID
	CharacterID      charactercontract.ID
	Action           string
	ItemID           contract.ItemID
	Slot             contract.Slot
	Page             int
	ExpectedRevision int64
	ExpiresAt        time.Time
}

func createControl(ctx context.Context, tx runtimecontract.DBTX, now time.Time, control inventoryControl) (string, error) {
	if tx == nil || control.UserID <= 0 || control.ChatID >= 0 || control.CharacterID.Validate() != nil || control.ExpectedRevision < 1 || control.Page < 0 {
		return "", contract.ErrInvalidInput
	}

	if control.Action != "page" && control.Action != "detail" && control.Action != "equip" && control.Action != "unequip" {
		return "", contract.ErrInvalidInput
	}

	mutation := control.Action == "equip" || control.Action == "unequip"
	if control.Action != "page" && control.ItemID.Validate() != nil || control.Action == "page" && control.ItemID != "" || mutation && !control.Slot.Valid() || !mutation && control.Slot != "" {
		return "", contract.ErrInvalidInput
	}

	owner, err := characterOwner(ctx, tx, control.CharacterID)
	if err != nil {
		return "", err
	}

	if owner != control.UserID {
		return "", contract.ErrNotFound
	}

	if err := pruneControls(ctx, tx, now); err != nil {
		return "", err
	}

	var random [16]byte

	if _, err := rand.Read(random[:]); err != nil {
		return "", contract.NewStorageError(err)
	}

	token := base64.RawURLEncoding.EncodeToString(random[:])
	var item, slot any

	if control.ItemID != "" {
		item = control.ItemID
	}
	if mutation {
		slot = control.Slot
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO inventory_controls(token,user_id,chat_id,character_id,action,item_id,slot,page,expected_revision,expires_at_ms) VALUES(?,?,?,?,?,?,?,?,?,?)`, token, control.UserID, control.ChatID, control.CharacterID, control.Action, item, slot, control.Page, control.ExpectedRevision, now.UTC().Add(controlLifetime).UnixMilli())
	if err != nil {
		return "", contract.NewStorageError(err)
	}

	return "inv:" + token, nil
}

func resolveControl(ctx context.Context, tx runtimecontract.DBTX, now time.Time, user runtimecontract.UserID, chat runtimecontract.ChatID, data string) (inventoryControl, error) {
	if tx == nil {
		return inventoryControl{}, contract.ErrInvalidInput
	}

	if user <= 0 || chat >= 0 || !strings.HasPrefix(data, "inv:") || len(data) != 26 {
		return inventoryControl{}, contract.ErrNotFound
	}

	token := strings.TrimPrefix(data, "inv:")
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 16 || base64.RawURLEncoding.EncodeToString(decoded) != token {
		return inventoryControl{}, contract.ErrNotFound
	}

	var control inventoryControl

	found := false
	err = queryInventoryRows(ctx, tx, `SELECT token,user_id,chat_id,character_id,action,item_id,slot,page,expected_revision,expires_at_ms FROM inventory_controls WHERE token=?`, []any{token}, func(rows *sql.Rows) error {
		var item, slot sql.NullString
		var expires int64

		if err := rows.Scan(&control.Token, &control.UserID, &control.ChatID, &control.CharacterID, &control.Action, &item, &slot, &control.Page, &control.ExpectedRevision, &expires); err != nil {
			return err
		}
		control.ItemID = contract.ItemID(item.String)
		control.Slot = contract.Slot(slot.String)
		control.ExpiresAt = time.UnixMilli(expires).UTC()
		found = true
		return nil
	})
	if err != nil {
		return inventoryControl{}, err
	}

	if !found || control.UserID != user || control.ChatID != chat {
		return inventoryControl{}, contract.ErrNotFound
	}

	owner, err := characterOwner(ctx, tx, control.CharacterID)
	if err != nil {
		return inventoryControl{}, err
	}

	if owner != user {
		return inventoryControl{}, contract.ErrNotFound
	}

	if !now.Before(control.ExpiresAt) {
		return control, contract.ErrExpiredControl
	}

	return control, nil
}

func pruneControls(ctx context.Context, tx runtimecontract.DBTX, now time.Time) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM inventory_controls WHERE token IN (SELECT token FROM inventory_controls WHERE expires_at_ms<=? ORDER BY expires_at_ms LIMIT ?)`, now.UTC().UnixMilli(), controlCleanupBatch)
	if err != nil {
		return contract.NewStorageError(err)
	}

	return nil
}
