package character_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func concurrentCharacterCalls(t *testing.T, count int, call func(int) error) {
	t.Helper()
	start := make(chan struct{})
	failures := make(chan error, count)
	var workers sync.WaitGroup

	for index := range count {
		workers.Go(func() {
			<-start
			if err := call(index); err != nil {
				failures <- err
			}
		})
	}

	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}

	if t.Failed() {
		t.FailNow()
	}
}

func TestConcurrentCreationMutationsAndMatchingReplays(t *testing.T) {
	service, db, _ := serviceFixture(t)
	const count = 16

	snapshots := make(chan contract.Snapshot, count)
	createdResults := make(chan bool, count)
	changes := make([]contract.HealthChange, count)
	concurrentCharacterCalls(t, count, func(index int) error {
		return db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
			snapshot, created, err := service.Ensure(ctx, tx, 42)
			if err != nil {
				return err
			}

			changes[index] = contract.HealthChange{OperationID: contract.OperationID(fmt.Sprintf("concurrent:damage:%d", index)), CharacterID: snapshot.ID, Kind: contract.HealthDamage, Amount: 2}
			result, err := service.ApplyHealth(ctx, tx, changes[index])
			if err != nil {
				return err
			}

			if !result.Applied {
				return errors.New("new concurrent operation reported replay")
			}

			snapshots <- result.Current
			createdResults <- created
			return nil
		})
	})
	close(snapshots)
	close(createdResults)
	var id contract.ID

	revisions := make(map[int64]bool)
	for snapshot := range snapshots {
		if id == "" {
			id = snapshot.ID
		}

		if snapshot.ID != id || snapshot.Health != 100-2*(snapshot.Revision-1) || snapshot.State != contract.StateActive || revisions[snapshot.Revision] {
			t.Fatalf("nonserializable state: %+v", snapshot)
		}

		revisions[snapshot.Revision] = true
	}

	createdCount := 0
	for created := range createdResults {
		if created {
			createdCount++
		}
	}

	if createdCount != 1 || len(revisions) != count {
		t.Fatalf("creation count=%d revisions=%d", createdCount, len(revisions))
	}

	concurrentCharacterCalls(t, count, func(index int) error {
		return db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
			result, err := service.ApplyHealth(ctx, tx, changes[index])
			if err != nil {
				return err
			}

			if result.Applied || result.Current.Health != 68 || result.Current.Revision != 17 || result.AppliedRevision < 2 || result.AppliedRevision > 17 {
				return fmt.Errorf("replay changed concurrent state: %+v", result)
			}

			return nil
		})
	})
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var owners, receipts int

		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM characters WHERE user_id=42`).Scan(&owners); err != nil {
			return err
		}

		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM character_operations`).Scan(&receipts); err != nil {
			return err
		}

		if owners != 1 || receipts != count {
			t.Fatalf("owners=%d receipts=%d", owners, receipts)
		}

		return nil
	})
}

func concurrencyService(t *testing.T, cfg character.Config, clock *testsupport.ManualClock) *character.Service {
	t.Helper()
	service, err := character.NewService(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}

	return service
}

func concurrencyApply(t *testing.T, db *persistence.DB, service *character.Service, change contract.HealthChange) contract.MutationResult {
	t.Helper()
	var result contract.MutationResult

	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		result, err = service.ApplyHealth(ctx, tx, change)
		return err
	})
	return result
}

func TestConcurrentRecoveryObservationsAndDamage(t *testing.T) {
	_, db, _ := serviceFixture(t)
	cfg, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}

	clock := testsupport.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	service := concurrencyService(t, cfg, clock)
	var snapshot contract.Snapshot

	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		snapshot, _, err = service.Ensure(ctx, tx, 42)
		return err
	})
	defeat := contract.HealthChange{OperationID: "recovery:defeat", CharacterID: snapshot.ID, Kind: contract.HealthDamage, Amount: 100}
	defeated := concurrencyApply(t, db, service, defeat)
	clock.Advance(defeated.Current.RecoverAt.Sub(clock.Now()))
	concurrentCharacterCalls(t, 12, func(index int) error {
		return db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
			var current contract.Snapshot

			if index%2 == 0 {
				var found bool
				var err error

				current, found, err = service.Lookup(ctx, tx, 42)
				if err != nil {
					return err
				}

				if !found {
					return errors.New("existing character missing")
				}
			} else {
				change := contract.HealthChange{OperationID: contract.OperationID(fmt.Sprintf("recovery:damage:%d", index)), CharacterID: snapshot.ID, Kind: contract.HealthDamage, Amount: 1}
				result, err := service.ApplyHealth(ctx, tx, change)
				if err != nil {
					return err
				}

				current = result.Current
			}
			if current.State != contract.StateActive || current.Health < 19 || current.Health > 25 || current.Revision < 3 || current.Revision > 9 {
				return fmt.Errorf("invalid concurrent recovery: %+v", current)
			}

			return nil
		})
	})
	replay := concurrencyApply(t, db, service, defeat)

	if replay.Applied || replay.AppliedRevision != 2 || replay.Current.Health != 19 || replay.Current.Revision != 9 {
		t.Fatalf("recovery repeated or damage lost: %+v", replay)
	}
}

func TestRestartReplayPreservesLaterDefeatAndStoredTuning(t *testing.T) {
	_, db, dir := serviceFixture(t)
	cfg, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}

	clock := testsupport.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	service := concurrencyService(t, cfg, clock)
	var initial contract.Snapshot

	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		var err error

		initial, _, err = service.Ensure(ctx, tx, 42)
		return err
	})
	damage := contract.HealthChange{OperationID: "restart:damage", CharacterID: initial.ID, Kind: contract.HealthDamage, Amount: 10}
	first := concurrencyApply(t, db, service, damage)
	defeat := contract.HealthChange{OperationID: "restart:defeat:1", CharacterID: initial.ID, Kind: contract.HealthDamage, Amount: 100}
	defeated := concurrencyApply(t, db, service, defeat)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openCharacterDB(t, dir)
	changed, err := character.ParseConfig([]byte(`{"max_health":200,"attack":20,"defense":15,"incapacitation_duration":"1h","recovery_percent":"50"}`))
	if err != nil {
		t.Fatal(err)
	}

	service = concurrencyService(t, changed, clock)
	clock.Advance(time.Minute)
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		stored, created, err := service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}

		if created || stored != defeated.Current {
			t.Fatalf("restart changed existing tuning/deadline: %+v", stored)
		}

		fresh, created, err := service.Ensure(ctx, tx, 43)
		if err != nil {
			return err
		}

		if !created || fresh.MaxHealth != 200 || fresh.Attack != 20 || fresh.Defense != 15 {
			t.Fatalf("new tuning not applied to new owner: %+v", fresh)
		}

		return nil
	})
	clock.Advance(defeated.Current.RecoverAt.Sub(clock.Now()))
	// Commit even after a domain rejection: conflicts must precede normalization.
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		conflict := damage
		conflict.Amount++
		if _, err := service.ApplyHealth(ctx, tx, conflict); !errors.Is(err, contract.ErrOperationConflict) {
			return fmt.Errorf("changed input accepted: %w", err)
		}

		var health, revision int64

		if err := tx.QueryRowContext(ctx, `SELECT health,revision FROM characters WHERE id=?`, initial.ID).Scan(&health, &revision); err != nil {
			return err
		}

		if health != 0 || revision != 3 {
			t.Fatal("conflict normalized character before rejection")
		}

		return nil
	})
	recovered := concurrencyApply(t, db, service, damage)

	if recovered.Applied || recovered.AppliedRevision != first.AppliedRevision || recovered.Current.Health != 25 || recovered.Current.Revision != 4 || recovered.Current.MaxHealth != 100 {
		t.Fatalf("stored recovery or replay changed: %+v", recovered)
	}

	laterDamage := damage
	laterDamage.OperationID = "restart:damage:later"
	laterDamage.Amount = 5
	later := concurrencyApply(t, db, service, laterDamage)
	replay := concurrencyApply(t, db, service, damage)

	if replay.Applied || replay.Current != later.Current || replay.Current.Health != 20 || replay.AppliedRevision != 2 {
		t.Fatalf("replay undid later damage: %+v", replay)
	}

	defeat.OperationID = "restart:defeat:2"
	secondDefeat := concurrencyApply(t, db, service, defeat)

	if !secondDefeat.Current.RecoverAt.Equal(clock.Now().Add(time.Hour)) {
		t.Fatal("new defeat did not use new duration")
	}

	replay = concurrencyApply(t, db, service, damage)

	if replay.Applied || replay.Current != secondDefeat.Current || replay.AppliedRevision != 2 {
		t.Fatal("old replay changed later defeat")
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openCharacterDB(t, dir)
	third, err := character.ParseConfig([]byte(`{"max_health":300,"attack":30,"defense":25,"incapacitation_duration":"1s","recovery_percent":"5"}`))
	if err != nil {
		t.Fatal(err)
	}

	service = concurrencyService(t, third, clock)
	replay = concurrencyApply(t, db, service, damage)

	if replay.Current != secondDefeat.Current {
		t.Fatal("second restart changed established defeat")
	}

	clock.Advance(time.Hour)
	replay = concurrencyApply(t, db, service, damage)

	if replay.Applied || replay.AppliedRevision != 2 || replay.Current.Health != 50 || replay.Current.MaxHealth != 100 || replay.Current.Attack != 10 || replay.Current.Defense != 5 || replay.Current.Revision != 7 || replay.Current.State != contract.StateActive {
		t.Fatalf("established recovery changed after tuning/restart: %+v", replay)
	}

	again := concurrencyApply(t, db, service, defeat)

	if again.Applied || again.Current != replay.Current || again.AppliedRevision != 6 {
		t.Fatalf("later defeat replay applied again: %+v", again)
	}
}
