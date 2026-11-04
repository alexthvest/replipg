package inventory

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	charactercontract "github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestConcurrentHealingSpendsOnlyOwnedCopies(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var before contract.Snapshot
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}
		before = opened.Current
		_, err = chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "prepare-damage", CharacterID: before.CharacterID, Kind: charactercontract.HealthDamage, Amount: 50})
		return err
	})
	type outcome struct {
		result contract.ConsumeResult
		err    error
	}
	const attempts = 16
	results := make(chan outcome, attempts)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() {
			<-start
			var out outcome
			err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				out.result, out.err = service.ConsumeHealing(ctx, tx, contract.ConsumeRequest{OperationID: contract.OperationID(fmt.Sprintf("concurrent-heal-%d", i)), CharacterID: before.CharacterID, ItemID: "healing_potion"})
				// Commit expected rejections as a gameplay caller may do.
				if errors.Is(out.err, contract.ErrNotOwned) {
					return nil
				}
				return out.err
			})
			if err != nil {
				out.err = err
			}
			results <- out
		})
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	revisions := make(map[int64]bool)
	for out := range results {
		if errors.Is(out.err, contract.ErrNotOwned) {
			rejected++
			continue
		}
		if out.err != nil {
			t.Fatal(out.err)
		}
		accepted++
		r := out.result.Mutation.AppliedRevision
		if !out.result.Mutation.Applied || revisions[r] || r < before.Revision+1 || r > before.Revision+3 || out.result.Mutation.Current.Revision != r {
			t.Fatalf("nonserializable result: %+v", out.result)
		}
		revisions[r] = true
		if out.result.Character.Health != 50+10*(r-before.Revision) || out.result.Character.Health > out.result.Character.MaxHealth {
			t.Fatalf("health differs from serialized consumption: %+v", out.result)
		}
		remaining := int64(0)
		for _, item := range out.result.Mutation.Current.Items {
			if item.Quantity <= 0 {
				t.Fatalf("nonpositive ownership: %+v", item)
			}
			if item.ItemID == "healing_potion" {
				remaining = item.Quantity
			}
		}
		if remaining != 3-(r-before.Revision) {
			t.Fatalf("remaining=%d at revision=%d", remaining, r)
		}
	}
	if accepted != 3 || rejected != attempts-3 {
		t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
	}
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		after, err := service.Inspect(ctx, tx, before.CharacterID)
		if err != nil {
			return err
		}
		ch, _, err := chars.Lookup(ctx, tx, 42)
		if err != nil {
			return err
		}
		if after.Revision != before.Revision+3 || ch.Health != 80 {
			t.Fatalf("final inventory=%+v character=%+v", after, ch)
		}
		var receipts int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_operations WHERE kind='consume'`).Scan(&receipts); err != nil {
			return err
		}
		if receipts != 3 {
			t.Fatalf("consumption receipts=%d", receipts)
		}
		return nil
	})
}

func TestDurableLaterStateReplayAfterControlPruning(t *testing.T) {
	db, catalog, clock, dir := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	service := inventoryService(t, catalog, chars, clock.Now)
	var grant contract.GrantRequest
	var equip contract.EquipmentChange
	var consume contract.ConsumeRequest
	var grantRevision, equipRevision, consumeRevision int64
	var current contract.Snapshot
	var currentCharacter charactercontract.Snapshot
	var expiredToken, liveToken string
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}
		id := opened.Current.CharacterID
		grant = contract.GrantRequest{OperationID: "original-grant", CharacterID: id, Items: []contract.GrantLine{{ItemID: "coin", Quantity: 7}}}
		granted, err := service.Grant(ctx, tx, grant)
		if err != nil {
			return err
		}
		grantRevision = granted.AppliedRevision
		for i := range controlCleanupBatch + 5 {
			token, err := createControl(ctx, tx, clock.Now(), inventoryControl{UserID: 42, ChatID: -1, CharacterID: id, Action: "equip", ItemID: "stick", Slot: contract.SlotWeapon, ExpectedRevision: grantRevision})
			if err != nil {
				return err
			}
			if i == 0 {
				expiredToken = token
			}
		}
		control, err := resolveControl(ctx, tx, clock.Now(), 42, -1, expiredToken)
		if err != nil {
			return err
		}
		equip = contract.EquipmentChange{OperationID: contract.OperationID("inventory:control:" + control.Token), CharacterID: id, Slot: control.Slot, ItemID: control.ItemID, ExpectedRevision: control.ExpectedRevision}
		equipped, err := service.ChangeEquipment(ctx, tx, equip)
		if err != nil {
			return err
		}
		equipRevision = equipped.AppliedRevision
		if _, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "damage", CharacterID: id, Kind: charactercontract.HealthDamage, Amount: 40}); err != nil {
			return err
		}
		consume = contract.ConsumeRequest{OperationID: "original-consume", CharacterID: id, ItemID: "healing_potion"}
		healed, err := service.ConsumeHealing(ctx, tx, consume)
		if err != nil {
			return err
		}
		consumeRevision = healed.Mutation.AppliedRevision
		later, err := service.Grant(ctx, tx, contract.GrantRequest{OperationID: "later-grant", CharacterID: id, Items: []contract.GrantLine{{ItemID: "coin", Quantity: 11}, {ItemID: "healing_potion", Quantity: 5}}})
		if err != nil {
			return err
		}
		unequipped, err := service.ChangeEquipment(ctx, tx, contract.EquipmentChange{OperationID: "later-unequip", CharacterID: id, Slot: contract.SlotWeapon, ExpectedRevision: later.Current.Revision})
		if err != nil {
			return err
		}
		current = unequipped.Current
		damaged, err := chars.ApplyHealth(ctx, tx, charactercontract.HealthChange{OperationID: "later-damage", CharacterID: id, Kind: charactercontract.HealthDamage, Amount: 20})
		currentCharacter = damaged.Current
		return err
	})
	clock.Advance(controlLifetime / 2)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error
		liveToken, err = createControl(ctx, tx, clock.Now(), inventoryControl{UserID: 42, ChatID: -1, CharacterID: current.CharacterID, Action: "page", ExpectedRevision: current.Revision})
		return err
	})
	clock.Advance(controlLifetime / 2)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db = openInventoryDB(t, dir)
	// Mechanics stay unchanged; future starter quantities are intentionally different.
	catalog.starter = append([]contract.GrantLine(nil), catalog.starter...)
	for i := range catalog.starter {
		catalog.starter[i].Quantity += 100
	}
	chars = inventoryCharacters(t, clock.Now)
	service = inventoryService(t, catalog, chars, clock.Now)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}
		if _, err := resolveControl(ctx, tx, clock.Now(), 42, -1, expiredToken); !errors.Is(err, contract.ErrExpiredControl) {
			t.Fatalf("expired accepted control: %v", err)
		}
		for _, wantCount := range []int{6, 1} {
			if err := pruneControls(ctx, tx, clock.Now()); err != nil {
				return err
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM inventory_controls`).Scan(&count); err != nil {
				return err
			}
			if count != wantCount {
				t.Fatalf("bounded pruning retained=%d want=%d", count, wantCount)
			}
		}
		if _, err := resolveControl(ctx, tx, clock.Now(), 42, -1, expiredToken); !errors.Is(err, contract.ErrNotFound) {
			t.Fatalf("pruned control: %v", err)
		}
		if _, err := resolveControl(ctx, tx, clock.Now(), 42, -1, liveToken); err != nil {
			return err
		}
		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}
		if opened.StarterGranted || opened.CharacterCreated || !reflect.DeepEqual(opened.Current, current) {
			t.Fatalf("changed starter altered existing state: %+v", opened)
		}
		granted, err := service.Grant(ctx, tx, grant)
		if err != nil {
			return err
		}
		equipped, err := service.ChangeEquipment(ctx, tx, equip)
		if err != nil {
			return err
		}
		healed, err := service.ConsumeHealing(ctx, tx, consume)
		if err != nil {
			return err
		}
		for _, replay := range []struct {
			result contract.MutationResult
			want   int64
		}{{granted, grantRevision}, {equipped, equipRevision}, {healed.Mutation, consumeRevision}} {
			if replay.result.Applied || replay.result.AppliedRevision != replay.want || replay.result.AppliedRevision >= replay.result.Current.Revision || !reflect.DeepEqual(replay.result.Current, current) {
				t.Fatalf("later-state replay: %+v want original revision=%d", replay.result, replay.want)
			}
		}
		if healed.Character != currentCharacter || healed.Character.Health != 50 {
			t.Fatalf("replay healed again: %+v", healed.Character)
		}
		conflict := grant
		conflict.Items = []contract.GrantLine{{ItemID: "coin", Quantity: 8}}
		if _, err := service.Grant(ctx, tx, conflict); !errors.Is(err, contract.ErrOperationConflict) {
			t.Fatalf("receipt lost conflict authority: %v", err)
		}
		after, err := service.Inspect(ctx, tx, current.CharacterID)
		if err == nil && !reflect.DeepEqual(after, current) {
			t.Fatalf("replay changed persisted state: %+v", after)
		}
		return err
	})
}
