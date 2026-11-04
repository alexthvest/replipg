package inventory

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestHealingBoundsAndRejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage int64
		item   itemcontract.ItemID
		want   error
		health int64
	}{
		{"normal", 30, "healing_potion", nil, 80},
		{"overheal", 3, "healing_potion", nil, 100},
		{"full", 0, "healing_potion", contract.ErrFullHealth, 100},
		{"incapacitated", 100, "healing_potion", contract.ErrIncapacitated, 0},
		{"weapon", 30, "stick", contract.ErrNotUsable, 70},
		{"inert", 30, "coin", contract.ErrNotUsable, 70},
		{"undefined", 30, "undefined", contract.ErrInvalidInput, 70},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, catalog, clock, _ := inventoryFixture(t)
			chars := inventoryCharacters(t, clock.Now)
			service := inventoryService(t, catalog, chars, clock.Now)
			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if err := service.Prepare(ctx, tx); err != nil {
					return err
				}

				opened, err := service.Open(ctx, tx, 42)
				if err != nil {
					return err
				}

				id := opened.Current.CharacterID
				if _, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "damage", CharacterID: id, Kind: charactercontract.HealthDamage, Amount: tc.damage}); err != nil {
					return err
				}

				result, err := service.ConsumeHealing(ctx, tx, contract.ConsumeRequest{OperationID: "consume", CharacterID: id, ItemID: tc.item})
				if !errors.Is(err, tc.want) {
					t.Fatalf("consume error %v, want %v", err, tc.want)
				}

				var storage runtimecontract.StorageFailure

				if tc.want != nil && errors.As(err, &storage) {
					t.Fatalf("domain rejection classified as storage failure: %v", err)
				}

				current, _, err := chars.Lookup(ctx, tx, 42)
				if err != nil {
					return err
				}

				if current.Health != tc.health {
					t.Fatalf("health %+v", current)
				}

				qty, err := ownedQuantity(ctx, tx, id, "healing_potion")
				if err != nil {
					return err
				}

				wantQty := int64(3)

				if tc.want == nil {
					wantQty = 2

					if !result.Mutation.Applied || result.Mutation.AppliedRevision != 3 || result.HealingHP != 10 {
						t.Fatalf("result %+v", result)
					}
				}

				if qty != wantQty {
					t.Fatalf("quantity %d want %d", qty, wantQty)
				}

				return nil
			})
		})
	}
}

func TestHealingRecoveryDeadlineAndRollback(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var defeated charactercontract.Snapshot

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}

		result, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "defeat", CharacterID: opened.Current.CharacterID, Kind: charactercontract.HealthDamage, Amount: 100})
		defeated = result.Current
		return err
	})
	clock.Advance(defeated.RecoverAt.Sub(clock.Now()))
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		// Overflow occurs after character recovery and healing, so committing this
		// rejection proves both tentative character changes were rolled back.
		if _, err := tx.ExecContext(ctx, `UPDATE inventories SET revision=9223372036854775807 WHERE character_id=?`, defeated.ID); err != nil {
			return err
		}

		_, err := service.ConsumeHealing(ctx, tx, contract.ConsumeRequest{OperationID: "recover", CharacterID: defeated.ID, ItemID: "healing_potion"})
		if !errors.Is(err, contract.ErrExhausted) {
			t.Fatalf("overflow: %v", err)
		}

		var health, revision int64

		if err := queryInventoryRows(ctx, tx, `SELECT health,revision FROM characters WHERE id=?`, []any{defeated.ID}, func(rows *sql.Rows) error { return rows.Scan(&health, &revision) }); err != nil {
			return err
		}

		if health != 0 || revision != defeated.Revision {
			t.Fatalf("recovery leaked %d %d", health, revision)
		}

		_, err = tx.ExecContext(ctx, `UPDATE inventories SET revision=2 WHERE character_id=?`, defeated.ID)
		return err
	})
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		result, err := service.ConsumeHealing(ctx, tx, contract.ConsumeRequest{OperationID: "recover", CharacterID: defeated.ID, ItemID: "healing_potion"})
		if err == nil && (result.Character.Health != 35 || result.Character.State != charactercontract.StateActive) {
			t.Fatalf("deadline result %+v", result)
		}

		return err
	})
}

func TestHealingLastCopyReplayRestartAndConflict(t *testing.T) {
	db, catalog, clock, dir := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var request contract.ConsumeRequest

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}

		request = contract.ConsumeRequest{OperationID: "consume", CharacterID: opened.Current.CharacterID, ItemID: "healing_potion"}
		if err := decreaseQuantity(ctx, tx, request.CharacterID, request.ItemID, 2); err != nil {
			return err
		}

		if _, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "damage", CharacterID: request.CharacterID, Kind: charactercontract.HealthDamage, Amount: 10}); err != nil {
			return err
		}

		result, err := service.ConsumeHealing(ctx, tx, request)
		if err != nil {
			return err
		}

		if result.Character.Health != 100 || !result.Mutation.Applied {
			t.Fatalf("first %+v", result)
		}

		for _, item := range result.Mutation.Current.Items {
			if item.ItemID == request.ItemID {
				t.Fatal("last copy still listed")
			}
		}

		// Replay at full HP and with no remaining copies still succeeds.
		again, err := service.ConsumeHealing(ctx, tx, request)
		if err != nil {
			return err
		}

		if again.Mutation.Applied || again.Mutation.AppliedRevision != 3 {
			t.Fatalf("replay %+v", again)
		}

		fresh := request
		fresh.OperationID = "fresh"
		if _, err := service.ConsumeHealing(ctx, tx, fresh); !errors.Is(err, contract.ErrNotOwned) {
			t.Fatalf("exhaustion %v", err)
		}

		_, err = chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "later-damage", CharacterID: request.CharacterID, Kind: charactercontract.HealthDamage, Amount: 30})
		return err
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openInventoryDB(t, dir)
	service = inventoryService(t, catalog, chars, clock.Now)
	inventoryTx(t, reopened, func(ctx context.Context, tx runtimecontract.DBTX) error {
		replay, err := service.ConsumeHealing(ctx, tx, request)
		if err != nil {
			return err
		}

		if replay.Mutation.Applied || replay.Mutation.AppliedRevision != 3 || replay.Character.Health != 70 || replay.HealingHP != 10 {
			t.Fatalf("restart replay %+v", replay)
		}

		conflicting := request
		conflicting.CharacterID = "other"
		if _, err := service.ConsumeHealing(ctx, tx, conflicting); !errors.Is(err, contract.ErrOperationConflict) {
			t.Fatalf("conflict %v", err)
		}

		for _, item := range []itemcontract.ItemID{"coin", "stick", "unknown"} {
			conflicting = request
			conflicting.ItemID = item
			if _, err := service.ConsumeHealing(ctx, tx, conflicting); !errors.Is(err, contract.ErrOperationConflict) {
				t.Fatalf("changed item %s: %v", item, err)
			}
		}
		return nil
	})
}

func TestHealingOrphanCharacterReceiptRollsBack(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}

		id := opened.Current.CharacterID
		if _, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "damage", CharacterID: id, Kind: charactercontract.HealthDamage, Amount: 50}); err != nil {
			return err
		}

		if _, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: healingOperationID("orphan"), CharacterID: id, Kind: charactercontract.HealthHeal, Amount: 10}); err != nil {
			return err
		}

		_, err = service.ConsumeHealing(ctx, tx, contract.ConsumeRequest{OperationID: "orphan", CharacterID: id, ItemID: "healing_potion"})
		var storage runtimecontract.StorageFailure

		if !errors.As(err, &storage) {
			t.Fatalf("orphan invariant %v", err)
		}

		qty, err := ownedQuantity(ctx, tx, id, "healing_potion")
		if err == nil && qty != 3 {
			t.Fatalf("spent orphan %d", qty)
		}

		return err
	})
}

type healingCharacterProbe struct {
	characterPort
	lookup func(charactercontract.Snapshot) charactercontract.Snapshot
	apply  func(context.Context, runtimecontract.DBTX, charactercontract.HealthChange) (charactercontract.MutationResult, error)
	calls  int
}

func (p *healingCharacterProbe) Lookup(ctx context.Context, tx runtimecontract.DBTX, user runtimecontract.UserID) (charactercontract.Snapshot, bool, error) {
	ch, found, err := p.characterPort.Lookup(ctx, tx, user)
	if err == nil && found && p.lookup != nil {
		ch = p.lookup(ch)
	}
	return ch, found, err
}

func (p *healingCharacterProbe) ApplyHealth(ctx context.Context, tx runtimecontract.DBTX, req charactercontract.HealthChange) (charactercontract.MutationResult, error) {
	p.calls++
	if p.apply != nil {
		return p.apply(ctx, tx, req)
	}

	return p.characterPort.ApplyHealth(ctx, tx, req)
}

func TestHealingIdentityAndCancellation(t *testing.T) {
	for _, mode := range []string{"lookup-identity", "apply-identity", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			db, catalog, clock, _ := inventoryFixture(t)
			chars := inventoryCharacters(t, clock.Now)
			probe := &healingCharacterProbe{characterPort: chars}
			service := inventoryService(t, catalog, probe, clock.Now)
			var id charactercontract.ID

			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				if err := service.Prepare(ctx, tx); err != nil {
					return err
				}

				opened, err := service.Open(ctx, tx, 42)
				if err != nil {
					return err
				}

				id = opened.Current.CharacterID
				_, err = chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "damage", CharacterID: id, Kind: charactercontract.HealthDamage, Amount: 30})
				return err
			})
			if mode == "lookup-identity" {
				probe.lookup = func(ch charactercontract.Snapshot) charactercontract.Snapshot { ch.UserID++; return ch }
			}
			if mode == "apply-identity" {
				probe.apply = func(ctx context.Context, tx runtimecontract.DBTX, req charactercontract.HealthChange) (charactercontract.MutationResult, error) {
					result, err := chars.ApplyHealth(ctx, tx, req)
					result.Current.ID = "wrong"
					return result, err
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			if mode == "cancel" {
				probe.apply = func(ctx context.Context, tx runtimecontract.DBTX, req charactercontract.HealthChange) (charactercontract.MutationResult, error) {
					result, err := chars.ApplyHealth(ctx, tx, req)
					if err != nil {
						return result, err
					}

					cancel()
					return result, ctx.Err()
				}
			}
			err := db.WithinTransaction(context.Background(), func(_ context.Context, tx runtimecontract.DBTX) error {
				_, err := service.ConsumeHealing(ctx, tx, contract.ConsumeRequest{OperationID: "consume", CharacterID: id, ItemID: "healing_potion"})
				return err
			})
			var storage runtimecontract.StorageFailure

			if !errors.As(err, &storage) {
				t.Fatalf("expected storage failure %v", err)
			}

			inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
				ch, _, err := chars.Lookup(ctx, tx, 42)
				if err != nil {
					return err
				}

				qty, err := ownedQuantity(ctx, tx, id, "healing_potion")
				if err != nil {
					return err
				}

				if ch.Health != 70 || qty != 3 {
					t.Fatalf("leaked state %+v qty %d", ch, qty)
				}

				var count int

				return queryInventoryRows(ctx, tx, `SELECT count(*) FROM inventory_operations WHERE operation_id='consume'`, nil, func(rows *sql.Rows) error {
					if err := rows.Scan(&count); err != nil {
						return err
					}

					if count != 0 {
						t.Fatalf("receipt leaked")
					}

					return nil
				})
			})
		})
	}
}

func TestHealingEffectAndAbsentTarget(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	effect, err := service.Effect("healing_potion")
	if err != nil || effect.HealingHP != 10 || effect.Kind != itemcontract.EffectHealSelfFixedHP {
		t.Fatalf("effect %+v %v", effect, err)
	}

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := service.ConsumeHealing(ctx, tx, contract.ConsumeRequest{OperationID: "missing", CharacterID: "missing", ItemID: "healing_potion"})
		if !errors.Is(err, contract.ErrNotFound) {
			t.Fatalf("missing %v", err)
		}

		return nil
	})
}
