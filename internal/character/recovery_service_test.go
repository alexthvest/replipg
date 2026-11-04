package character_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func TestRecoveryNormalizesAtBoundaryAndOnlyOnce(t *testing.T) {
	_, db, dir := serviceFixture(t)
	deadline := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	clock := testsupport.NewManualClock(deadline.Add(-time.Nanosecond))
	cfg, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}

	service, err := character.NewService(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}

	var initial contract.Snapshot

	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		initial, _, err = service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `UPDATE characters SET health=0,recover_at_ms=?,recovery_health=17,revision=2 WHERE id=?`, deadline.UnixMilli(), initial.ID)
		return err
	})
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, err := service.Eligibility(ctx, tx, 42)
		if err != nil {
			return err
		}

		if got.Kind != contract.EligibilityIncapacitated || got.Character.Health != 0 || got.Character.RecoverAt != deadline || got.Character.Revision != 2 {
			t.Fatalf("recovered before deadline: %+v", got)
		}

		return nil
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openCharacterDB(t, dir)
	clock.Advance(time.Nanosecond)
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, created, err := service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}

		if created || got.ID != initial.ID || got.Health != 17 || got.State != contract.StateActive || !got.RecoverAt.IsZero() || got.Revision != 3 {
			t.Fatalf("incorrect boundary recovery: %+v", got)
		}

		_, err = tx.ExecContext(ctx, `UPDATE characters SET health=9,revision=4 WHERE id=?`, got.ID)
		return err
	})
	clock.Advance(24 * time.Hour)
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for range 2 {
			got, found, err := service.Lookup(ctx, tx, 42)
			if err != nil {
				return err
			}

			if !found || got.Health != 9 || got.Revision != 4 {
				t.Fatalf("recovery repeated or passive regeneration: %+v", got)
			}
		}

		got, err := service.Eligibility(ctx, tx, 42)
		if err != nil {
			return err
		}

		if got.Kind != contract.EligibilityActive || got.Character.Health != 9 {
			t.Fatalf("incorrect active eligibility: %+v", got)
		}

		return nil
	})
}

func TestRecoveryConcurrentObservationsAndClockReads(t *testing.T) {
	_, db, _ := serviceFixture(t)
	deadline := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	cfg, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}

	clock := testsupport.NewManualClock(deadline)
	service, err := character.NewService(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}

	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		initial, _, err := service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}

		_, err = tx.ExecContext(ctx, `UPDATE characters SET health=0,recover_at_ms=?,recovery_health=25,revision=2 WHERE id=?`, deadline.UnixMilli(), initial.ID)
		return err
	})
	var wg sync.WaitGroup

	failures := make(chan error, 8)
	snapshots := make(chan contract.Snapshot, 8)
	for range 8 {
		wg.Go(func() {
			err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
				got, found, err := service.Lookup(ctx, tx, 42)
				if err != nil {
					return err
				}

				if !found {
					t.Error("missing existing character")
				}

				snapshots <- got
				return nil
			})
			if err != nil {
				failures <- err
			}
		})
	}

	wg.Wait()
	close(failures)
	close(snapshots)
	for err := range failures {
		t.Fatal(err)
	}

	for got := range snapshots {
		if got.Health != 25 || got.Revision != 3 {
			t.Fatalf("recovered more than once: %+v", got)
		}
	}

	reads := 0
	counted, err := character.NewService(cfg, func() time.Time { reads++; return deadline })
	if err != nil {
		t.Fatal(err)
	}

	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, _, err := counted.Ensure(ctx, tx, 42); err != nil {
			return err
		}

		if reads != 1 {
			t.Fatalf("Ensure clock reads %d", reads)
		}

		if _, _, err := counted.Lookup(ctx, tx, 42); err != nil {
			return err
		}

		if reads != 2 {
			t.Fatalf("Lookup clock reads %d", reads)
		}

		if _, err := counted.Eligibility(ctx, tx, 42); err != nil {
			return err
		}

		if reads != 3 {
			t.Fatalf("Eligibility clock reads %d", reads)
		}

		missing, err := counted.Eligibility(ctx, tx, 999)
		if err != nil {
			return err
		}

		if missing.Kind != contract.EligibilityMissing || missing.Character != (contract.Snapshot{}) {
			t.Fatal("missing eligibility created state")
		}

		return nil
	})
}
