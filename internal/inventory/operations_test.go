package inventory

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"testing"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestCanonicalInventoryRequests(t *testing.T) {
	req := contract.GrantRequest{OperationID: "test:grant", CharacterID: storeCharacterID, Items: []contract.GrantLine{{ItemID: "z", Quantity: 2}, {ItemID: "a", Quantity: 1}, {ItemID: "z", Quantity: 3}}}
	got, err := canonicalGrant(req)
	if err != nil {
		t.Fatal(err)
	}

	if got != `{"version":1,"items":[{"item_id":"a","quantity":1},{"item_id":"z","quantity":5}]}` {
		t.Fatalf("canonical=%s", got)
	}

	req.Items = []contract.GrantLine{{ItemID: "z", Quantity: 5}, {ItemID: "a", Quantity: 1}}
	equivalent, err := canonicalGrant(req)
	if err != nil || equivalent != got {
		t.Fatalf("equivalence=%s err=%v", equivalent, err)
	}

	req.Items = []contract.GrantLine{{ItemID: "z", Quantity: math.MaxInt64}, {ItemID: "z", Quantity: 1}}
	if _, err := canonicalGrant(req); !errors.Is(err, contract.ErrExhausted) {
		t.Fatalf("overflow=%v", err)
	}

	req.Items[1].Quantity = 0
	if _, err := canonicalGrant(req); !errors.Is(err, contract.ErrInvalidInput) {
		t.Fatalf("invalid=%v", err)
	}

	equip := contract.EquipmentChange{OperationID: "equip:1", CharacterID: storeCharacterID, Slot: contract.SlotWeapon, ExpectedRevision: 2}
	first, err := canonicalEquipment(equip)
	if err != nil {
		t.Fatal(err)
	}

	equip.ExpectedRevision++
	second, err := canonicalEquipment(equip)
	if err != nil || first == second {
		t.Fatal("equipment revision omitted")
	}

	consume := contract.ConsumeRequest{OperationID: "consume:1", CharacterID: storeCharacterID, ItemID: "potion"}
	effect := contract.EffectDefinition{ItemID: "potion", Kind: contract.EffectHealSelfFixedHP, HealingHP: 10}
	first, err = canonicalConsume(consume, effect)
	if err != nil {
		t.Fatal(err)
	}

	effect.HealingHP++
	second, err = canonicalConsume(consume, effect)
	if err != nil || first == second {
		t.Fatal("effect omitted")
	}

	effect.ItemID = "other"
	if _, err := canonicalConsume(consume, effect); !errors.Is(err, contract.ErrInvalidInput) {
		t.Fatalf("effect mismatch=%v", err)
	}

	if starterOperationID(storeCharacterID) != contract.OperationID("inventory:starter:"+string(storeCharacterID)) {
		t.Fatal("starter identity")
	}

	heal := healingOperationID(contract.OperationID(strings.Repeat("x", 128)))
	if err := heal.Validate(); err != nil {
		t.Fatal(err)
	}

	if heal != healingOperationID(contract.OperationID(strings.Repeat("x", 128))) || heal == healingOperationID("other") {
		t.Fatal("healing identity")
	}
}

func TestInventoryReceiptsReplayConflictAndReopen(t *testing.T) {
	db, _, _, dir := inventoryFixture(t)
	receipt := operationRecord{OperationID: "test:receipt", CharacterID: storeCharacterID, Kind: "grant", CanonicalRequest: `{"version":1,"items":[]}`, AppliedRevision: 3}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := seedStoreCharacter(ctx, tx); err != nil {
			return err
		}

		if _, found, err := checkOperation(ctx, tx, receipt); err != nil || found {
			t.Fatalf("missing found=%v err=%v", found, err)
		}

		return recordOperation(ctx, tx, receipt)
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openInventoryDB(t, dir)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, found, err := checkOperation(ctx, tx, receipt)
		if err != nil || !found || got != receipt {
			t.Fatalf("replay=%+v found=%v err=%v", got, found, err)
		}

		for _, change := range []func(*operationRecord){
			func(r *operationRecord) { r.CharacterID = charactercontract.ID("abcdef0123456789abcdef0123456789") },
			func(r *operationRecord) { r.Kind = "starter" },
			func(r *operationRecord) { r.CanonicalRequest = `{"version":2}` },
		} {
			other := receipt
			change(&other)
			if _, found, err := checkOperation(ctx, tx, other); found || !errors.Is(err, contract.ErrOperationConflict) {
				t.Fatalf("conflict found=%v err=%v", found, err)
			}
		}

		consume := receipt
		consume.OperationID = "test:consume"
		consume.Kind = "consume"
		consume.EffectKind = contract.EffectHealSelfFixedHP
		consume.HealingHP = 10
		if err := recordOperation(ctx, tx, consume); err != nil {
			return err
		}

		consume.HealingHP++
		if _, found, err := checkOperation(ctx, tx, consume); found || !errors.Is(err, contract.ErrOperationConflict) {
			t.Fatalf("frozen effect conflict=%v", err)
		}

		var count int

		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_operations`).Scan(&count); err != nil {
			return err
		}

		if count != 2 {
			t.Fatal("replay changed receipts")
		}

		return nil
	})
}

func TestInventoryOperationRejectThenOuterCommit(t *testing.T) {
	db, _, _, _ := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := seedStoreCharacter(ctx, tx); err != nil {
			return err
		}

		err := withInventoryOperation(ctx, tx, func() error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO inventories VALUES(?,1,1)`, storeCharacterID); err != nil {
				return err
			}

			if err := recordOperation(ctx, tx, operationRecord{OperationID: "rejected", CharacterID: storeCharacterID, Kind: "starter", CanonicalRequest: `{"version":1}`, AppliedRevision: 1}); err != nil {
				return err
			}

			return &contract.DomainError{Kind: contract.ErrorNotOwned}
		})
		if !errors.Is(err, contract.ErrNotOwned) {
			t.Fatalf("rejection=%v", err)
		}

		// A successful sibling operation is retained by the outer transaction.
		return withInventoryOperation(ctx, tx, func() error {
			_, err := tx.ExecContext(ctx, `INSERT INTO inventories VALUES(?,2,0)`, storeCharacterID)
			return err
		})
	})
	var revision, count int

	if err := db.QueryRowContext(context.Background(), `SELECT revision FROM inventories`).Scan(&revision); err != nil || revision != 2 {
		t.Fatalf("revision=%d err=%v", revision, err)
	}

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM inventory_operations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("receipt survived=%d err=%v", count, err)
	}
}

type operationFailureTX struct {
	runtimecontract.DBTX
	fail  string
	cause error
	calls []string
}

func (tx *operationFailureTX) ExecContext(_ context.Context, query string, _ ...any) (sql.Result, error) {
	tx.calls = append(tx.calls, query)
	if strings.HasPrefix(query, tx.fail) {
		return nil, tx.cause
	}

	return nil, nil
}

func (tx *operationFailureTX) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, tx.cause
}

func TestInventoryOperationStorageFailures(t *testing.T) {
	cause := errors.New("private database arguments")
	rejected := &contract.DomainError{Kind: contract.ErrorFullHealth}
	for _, prefix := range []string{"SAVEPOINT", "ROLLBACK", "RELEASE"} {
		t.Run(prefix, func(t *testing.T) {
			tx := &operationFailureTX{fail: prefix, cause: cause}
			called := false
			err := withInventoryOperation(context.Background(), tx, func() error { called = true; return rejected })
			var marker runtimecontract.StorageFailure

			if !errors.Is(err, cause) || !errors.Is(err, contract.ErrStorage) || !errors.As(err, &marker) {
				t.Fatalf("classification=%v", err)
			}

			if strings.Contains(err.Error(), cause.Error()) {
				t.Fatal("unsafe storage text")
			}

			if prefix == "SAVEPOINT" && called {
				t.Fatal("action after failed savepoint")
			}

			if prefix != "SAVEPOINT" && !errors.Is(err, rejected) {
				t.Fatal("lost rejection")
			}
		})
	}

	tx := &operationFailureTX{fail: "INSERT", cause: cause}
	record := operationRecord{OperationID: "failure", CharacterID: storeCharacterID, Kind: "grant", CanonicalRequest: `{"version":1}`, AppliedRevision: 1}
	if err := recordOperation(context.Background(), tx, record); !errors.Is(err, cause) || !errors.Is(err, contract.ErrStorage) {
		t.Fatalf("insert=%v", err)
	}

	if _, _, err := checkOperation(context.Background(), tx, record); !errors.Is(err, cause) || !errors.Is(err, contract.ErrStorage) {
		t.Fatalf("query=%v", err)
	}
}

func TestInventoryOperationUnexpectedActionAndSuccessReleaseFailures(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("private driver failure")} {
		tx := &operationFailureTX{fail: "never", cause: cause}
		err := withInventoryOperation(context.Background(), tx, func() error { return cause })
		var marker runtimecontract.StorageFailure

		if !errors.Is(err, cause) || !errors.Is(err, contract.ErrStorage) || !errors.As(err, &marker) {
			t.Fatalf("action classification=%v", err)
		}

		if len(tx.calls) != 3 || !strings.HasPrefix(tx.calls[1], "ROLLBACK") || !strings.HasPrefix(tx.calls[2], "RELEASE") {
			t.Fatalf("cleanup sequence=%v", tx.calls)
		}
	}

	cause := errors.New("release failed")
	tx := &operationFailureTX{fail: "RELEASE", cause: cause}
	if err := withInventoryOperation(context.Background(), tx, func() error { return nil }); !errors.Is(err, cause) || !errors.Is(err, contract.ErrStorage) {
		t.Fatalf("success release=%v", err)
	}
}

func TestInventoryReceiptOuterRollback(t *testing.T) {
	db, _, _, _ := inventoryFixture(t)
	inventoryTx(t, db, seedStoreCharacter)
	abort := errors.New("outer transition rejected")
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := withInventoryOperation(ctx, tx, func() error {
			return recordOperation(ctx, tx, operationRecord{OperationID: "rolled-back", CharacterID: storeCharacterID, Kind: "grant", CanonicalRequest: `{"version":1}`, AppliedRevision: 1})
		}); err != nil {
			return err
		}

		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("outer rejection=%v", err)
	}

	var count int

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM inventory_operations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial receipt count=%d err=%v", count, err)
	}
}

func TestOperationExpectedSentinelsRemainDomainFailures(t *testing.T) {
	db, _, _, _ := inventoryFixture(t)
	for _, expected := range []error{contract.ErrNotOwned, contract.ErrIncompatibleSlot, contract.ErrStaleControl, contract.ErrExpiredControl, contract.ErrNotUsable, contract.ErrFullHealth, contract.ErrInvalidInput, contract.ErrInvalidCatalog, contract.ErrNotFound, contract.ErrIncapacitated, contract.ErrOperationConflict, contract.ErrExhausted} {
		inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
			err := withInventoryOperation(ctx, tx, func() error { return expected })
			var storage runtimecontract.StorageFailure

			if !errors.Is(err, expected) || errors.As(err, &storage) {
				t.Fatalf("domain sentinel became storage failure: %v", err)
			}

			return nil
		})
	}
}
