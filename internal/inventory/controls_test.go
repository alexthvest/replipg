package inventory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestControlsBindingExpiryAndRestart(t *testing.T) {
	db, catalog, clock, dir := inventoryFixture(t)
	var token string
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := prepareCatalog(ctx, tx, catalog); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO characters VALUES(?,42,100,100,10,5,NULL,NULL,1); INSERT INTO inventories VALUES(?,1,0)`, inventoryTestCharacter, inventoryTestCharacter); err != nil {
			return err
		}
		var err error
		token, err = createControl(ctx, tx, clock.Now(), inventoryControl{UserID: 42, ChatID: -1, CharacterID: inventoryTestCharacter, Action: "detail", ItemID: "coin", ExpectedRevision: 1})
		return err
	})
	if len(token) != 26 || !strings.HasPrefix(token, "inv:") {
		t.Fatalf("payload %q", token)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openInventoryDB(t, dir)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		control, err := resolveControl(ctx, tx, clock.Now(), 42, -1, token)
		if err != nil {
			return err
		}
		if control.ItemID != "coin" || control.Action != "detail" || !control.ExpiresAt.Equal(clock.Now().Add(5*time.Minute)) {
			t.Fatalf("control %+v", control)
		}
		for _, tc := range []struct {
			user  runtimecontract.UserID
			chat  runtimecontract.ChatID
			token string
		}{{43, -1, token}, {42, -2, token}, {42, -1, "inv:bad"}, {42, -1, "inv:" + strings.Repeat("!", 22)}, {42, -1, "inv:" + strings.Repeat("a", 22)}} {
			if _, err := resolveControl(ctx, tx, clock.Now(), tc.user, tc.chat, tc.token); !errors.Is(err, contract.ErrNotFound) {
				t.Fatalf("unsafe control %v", err)
			}
		}
		if _, err := resolveControl(ctx, tx, clock.Now().Add(5*time.Minute), 42, -1, token); !errors.Is(err, contract.ErrExpiredControl) {
			t.Fatalf("expiry equality %v", err)
		}
		return nil
	})
}

func TestControlsCleanupIsBoundedAndRetainsReceipts(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := prepareCatalog(ctx, tx, catalog); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO characters VALUES(?,42,100,100,10,5,NULL,NULL,1); INSERT INTO inventories VALUES(?,1,0)`, inventoryTestCharacter, inventoryTestCharacter); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_operations VALUES('permanent',?,'grant','{}',1,NULL,NULL)`, inventoryTestCharacter); err != nil {
			return err
		}
		for i := 0; i < controlCleanupBatch+5; i++ {
			token := fmt.Sprintf("%022d", i)
			if _, err := tx.ExecContext(ctx, `INSERT INTO inventory_controls VALUES(?,42,-1,?,'page',NULL,NULL,0,1,?)`, token, inventoryTestCharacter, clock.Now().UnixMilli()); err != nil {
				return err
			}
		}
		if err := pruneControls(ctx, tx, clock.Now()); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_controls`).Scan(&count); err != nil {
			return err
		}
		if count != 5 {
			t.Fatalf("unbounded cleanup remaining %d", count)
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_operations`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatal("receipt deleted")
		}
		// Display numbers are never callback authority; controls hold the item ID.
		data, err := createControl(ctx, tx, clock.Now(), inventoryControl{UserID: 42, ChatID: -1, CharacterID: inventoryTestCharacter, Action: "page", Page: 7, ExpectedRevision: 1})
		if err != nil {
			return err
		}
		control, err := resolveControl(ctx, tx, clock.Now(), 42, -1, data)
		if err != nil {
			return err
		}
		if control.Page != 7 || control.ItemID != "" {
			t.Fatalf("page %+v", control)
		}
		return nil
	})
}
