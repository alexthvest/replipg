package character_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/character/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func TestApplyHealthBoundsNoopsAndReplay(t *testing.T) {
	service, db, _ := serviceFixture(t)
	var id contract.ID
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, _, err := service.Ensure(ctx, tx, 42)
		id = got.ID
		return err
	})
	changes := []struct {
		operation                contract.OperationID
		kind                     contract.HealthKind
		amount, health, revision int64
	}{
		{"zero", contract.HealthDamage, 0, 100, 1},
		{"full-heal", contract.HealthHeal, 1<<63 - 1, 100, 1},
		{"damage", contract.HealthDamage, 70, 30, 2},
		{"heal", contract.HealthHeal, 1<<63 - 1, 100, 3},
	}
	for _, change := range changes {
		inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
			got, err := service.ApplyHealth(ctx, tx, contract.HealthChange{OperationID: change.operation, CharacterID: id, Kind: change.kind, Amount: change.amount})
			if err != nil {
				return err
			}
			if !got.Applied || got.AppliedRevision != change.revision || got.Current.Health != change.health || got.Current.Revision != change.revision {
				t.Fatalf("invalid health mutation: %+v", got)
			}
			return nil
		})
	}
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, err := service.ApplyHealth(ctx, tx, contract.HealthChange{OperationID: "damage", CharacterID: id, Kind: contract.HealthDamage, Amount: 70})
		if err != nil {
			return err
		}
		if got.Applied || got.AppliedRevision != 2 || got.Current.Health != 100 || got.Current.Revision != 3 {
			t.Fatalf("replay reapplied or returned old snapshot: %+v", got)
		}
		return nil
	})
}

func TestApplyHealthDefeatBlockRecoveryAndStoredReceipt(t *testing.T) {
	_, db, _ := serviceFixture(t)
	start := time.Date(2026, 1, 1, 0, 0, 0, 123456, time.UTC)
	clock := testsupport.NewManualClock(start)
	cfg, err := character.LoadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	service, err := character.NewService(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	var id contract.ID
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, _, err := service.Ensure(ctx, tx, 42)
		id = got.ID
		return err
	})
	defeat := contract.HealthChange{OperationID: "defeat", CharacterID: id, Kind: contract.HealthDamage, Amount: 1<<63 - 1}
	var defeated contract.Snapshot
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, err := service.ApplyHealth(ctx, tx, defeat)
		if err != nil {
			return err
		}
		defeated = got.Current
		if defeated.Health != 0 || defeated.State != contract.StateIncapacitated || defeated.Revision != 2 || defeated.RecoverAt.Before(start.Add(5*time.Second)) {
			t.Fatalf("invalid defeat: %+v", got)
		}
		return nil
	})
	blocked := contract.HealthChange{OperationID: "blocked", CharacterID: id, Kind: contract.HealthHeal, Amount: 1}
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := service.ApplyHealth(ctx, tx, blocked); !errors.Is(err, contract.ErrIncapacitated) {
			t.Fatalf("pending incapacitation accepted: %v", err)
		}
		got, err := service.ApplyHealth(ctx, tx, defeat)
		if err != nil {
			return err
		}
		if got.Applied || got.Current != defeated {
			t.Fatal("replay extended defeat")
		}
		return nil
	})
	clock.Advance(defeated.RecoverAt.Sub(start))
	// The rejected operation has no receipt and may now be accepted.
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, err := service.ApplyHealth(ctx, tx, blocked)
		if err != nil {
			return err
		}
		if !got.Applied || got.Current.Health != 26 || got.Current.State != contract.StateActive || got.Current.Revision != 4 {
			t.Fatalf("recovery+healing invalid: %+v", got)
		}
		replay, err := service.ApplyHealth(ctx, tx, defeat)
		if err != nil {
			return err
		}
		if replay.Applied || replay.AppliedRevision != 2 || replay.Current != got.Current {
			t.Fatal("old defeat replay reset health")
		}
		return nil
	})
}

func TestApplyHealthRejectsConflictBeforeRecoveryAndInvalidTargets(t *testing.T) {
	_, db, _ := serviceFixture(t)
	clock := testsupport.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cfg, err := character.LoadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	service, err := character.NewService(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	var id contract.ID
	var deadline time.Time
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, _, err := service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}
		id = got.ID
		result, err := service.ApplyHealth(ctx, tx, contract.HealthChange{OperationID: "defeat", CharacterID: id, Kind: contract.HealthDamage, Amount: 100})
		deadline = result.Current.RecoverAt
		return err
	})
	clock.Advance(deadline.Sub(clock.Now()))
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for _, change := range []contract.HealthChange{
			{OperationID: "defeat", CharacterID: id, Kind: contract.HealthDamage, Amount: 99},
			{OperationID: "defeat", CharacterID: id, Kind: contract.HealthHeal, Amount: 100},
			{OperationID: "defeat", CharacterID: "other-target", Kind: contract.HealthDamage, Amount: 100},
		} {
			if _, err := service.ApplyHealth(ctx, tx, change); !errors.Is(err, contract.ErrOperationConflict) {
				t.Fatalf("changed replay accepted: %v", err)
			}
		}
		if _, err := service.ApplyHealth(ctx, tx, contract.HealthChange{OperationID: "invalid", CharacterID: id, Kind: contract.HealthDamage, Amount: -1}); !errors.Is(err, contract.ErrInvalidInput) {
			t.Fatalf("negative amount accepted: %v", err)
		}
		if _, err := service.ApplyHealth(ctx, tx, contract.HealthChange{OperationID: "missing", CharacterID: "absent", Kind: contract.HealthDamage, Amount: 1}); !errors.Is(err, contract.ErrNotFound) {
			t.Fatalf("missing target accepted: %v", err)
		}
		rows, err := tx.QueryContext(ctx, `SELECT health,revision FROM characters WHERE id=?`, id)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		var health, revision int64
		if !rows.Next() {
			t.Fatal("character missing")
		}
		if err := rows.Scan(&health, &revision); err != nil {
			return err
		}
		if health != 0 || revision != 2 {
			t.Fatal("rejected input normalized or mutated character")
		}
		return rows.Err()
	})
}

func TestApplyHealthClockReadOnce(t *testing.T) {
	_, db, _ := serviceFixture(t)
	cfg, err := character.LoadConfig(false)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	service, err := character.NewService(cfg, func() time.Time { reads++; return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, _, err := service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}
		reads = 0
		change := contract.HealthChange{OperationID: "op", CharacterID: got.ID, Kind: contract.HealthDamage, Amount: 1}
		if _, err := service.ApplyHealth(ctx, tx, change); err != nil {
			return err
		}
		if reads != 1 {
			t.Fatalf("new operation read clock %d times", reads)
		}
		if _, err := service.ApplyHealth(ctx, tx, change); err != nil {
			return err
		}
		if reads != 2 {
			t.Fatalf("replay read clock %d times total", reads)
		}
		return nil
	})
}

func TestApplyHealthReceiptFailureRollsBackMutationAndAllowsRetry(t *testing.T) {
	service, db, _ := serviceFixture(t)
	var original contract.Snapshot
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, _, err := service.Ensure(ctx, tx, 42)
		original = got
		return err
	})
	change := contract.HealthChange{OperationID: "retryable", CharacterID: original.ID, Kind: contract.HealthDamage, Amount: 40}
	failure := errors.New("sensitive receipt driver data")
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := service.ApplyHealth(ctx, failCharacterReceipt{DBTX: tx, failure: failure}, change)
		if !errors.Is(err, contract.ErrStorage) || !errors.Is(err, failure) {
			t.Fatalf("missing inspectable receipt failure: %v", err)
		}
		return err
	})
	if !errors.Is(err, failure) {
		t.Fatalf("lost rollback cause: %v", err)
	}
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, found, err := service.Lookup(ctx, tx, 42)
		if err != nil {
			return err
		}
		if !found || got != original {
			t.Fatal("receipt failure persisted health change")
		}
		applied, err := service.ApplyHealth(ctx, tx, change)
		if err != nil {
			return err
		}
		if !applied.Applied || applied.Current.Health != 60 || applied.Current.Revision != 2 {
			t.Fatalf("failed work consumed identity: %+v", applied)
		}
		return nil
	})
}

type failCharacterReceipt struct {
	runtimecontract.DBTX
	failure error
}

func (tx failCharacterReceipt) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if strings.HasPrefix(query, "INSERT INTO character_operations") {
		return nil, tx.failure
	}
	return tx.DBTX.ExecContext(ctx, query, args...)
}

func TestMatchingDefeatReplayNormalizesDueRecovery(t *testing.T) {
	_, db, _ := serviceFixture(t)
	clock := testsupport.NewManualClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	cfg, err := character.LoadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	service, err := character.NewService(cfg, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	var defeat contract.HealthChange
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, _, err := service.Ensure(ctx, tx, 42)
		if err != nil {
			return err
		}
		defeat = contract.HealthChange{OperationID: "defeat-replay", CharacterID: got.ID, Kind: contract.HealthDamage, Amount: 100}
		_, err = service.ApplyHealth(ctx, tx, defeat)
		return err
	})
	clock.Advance(5 * time.Second)
	inCharacterTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, err := service.ApplyHealth(ctx, tx, defeat)
		if err != nil {
			return err
		}
		if got.Applied || got.AppliedRevision != 2 || got.Current.Health != 25 || got.Current.Revision != 3 || got.Current.State != contract.StateActive || !got.Current.RecoverAt.IsZero() {
			t.Fatalf("replay failed effective recovery: %+v", got)
		}
		return nil
	})
}
