package sharing

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/sharing/contract"
)

func sharingTestDB(t *testing.T) *persistence.DB {
	t.Helper()
	ctx := context.Background()
	db, err := persistence.Open(ctx, persistence.Options{StorageDir: t.TempDir(), StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	m, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Migrate(ctx, m); err != nil {
		t.Fatal(err)
	}
	return db
}
func fixtureView() viewRecord {
	return viewRecord{Token: strings.Repeat("a", 32), Owner: 42, Destination: runtimecontract.GroupDestination{ChatID: -1, Kind: runtimecontract.ChatKindSupergroup}, Kind: contract.InventoryPage, Locale: localization.English, FingerprintVersion: 1, BodyHash: make([]byte, 32), UsableUntil: time.Unix(200, 0).UTC(), CreatedAt: time.Unix(100, 0).UTC()}
}
func fixtureAction(id runtimecontract.UpdateID, token string) actionRecord {
	return actionRecord{UpdateID: id, ViewToken: token, PublicDeliveryID: runtimecontract.DeliveryID("public-" + time.Unix(int64(id), 0).Format("150405")), AcceptedAt: time.Unix(100, 0).UTC(), NextCheckAt: time.Unix(101, 0).UTC(), CallbackQueryID: "callback", CallbackDeadline: time.Unix(115, 0).UTC()}
}
func mustTx(t *testing.T, db *persistence.DB, fn func(context.Context, runtimecontract.DBTX) error) {
	t.Helper()
	if err := db.WithinTransaction(context.Background(), fn); err != nil {
		t.Fatal(err)
	}
}
func TestStoreReservationsRollbackAndReplay(t *testing.T) {
	db := sharingTestDB(t)
	v := fixtureView()
	a := fixtureAction(1, v.Token)
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := insertView(ctx, tx, v); err != nil {
			return err
		}
		return insertAction(ctx, tx, a)
	})
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		stored, err := readView(ctx, tx, v.Token)
		if err != nil {
			return err
		}
		if stored.Owner != 42 || !stored.UsableUntil.Equal(v.UsableUntil) || len(stored.BodyHash) != 32 || stored.Revision != 1 {
			t.Fatal("view roundtrip")
		}
		for _, candidate := range []actionRecord{a, fixtureAction(2, v.Token)} {
			if err := insertAction(ctx, tx, candidate); err == nil {
				t.Fatal("accepted conflicting reservation")
			}
		}
		got, err := readAction(ctx, tx, 1)
		if err != nil {
			return err
		}
		if got.PublicState != "pending" || got.CallbackQueryID != "callback" || !got.CallbackDeadline.Equal(a.CallbackDeadline) {
			t.Fatal("action roundtrip")
		}
		got.PublicState = "succeeded"
		got.PublicOutcomeAt = time.Unix(102, 0).UTC()
		got.PublicPurgeAfter = got.PublicOutcomeAt.Add(24 * time.Hour)
		got.FeedbackState = "skipped"
		got.CallbackQueryID = ""
		got.CallbackDeadline = time.Time{}
		return updateAction(ctx, tx, got)
	})
	rollback := errors.New("rollback")
	err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := insertAction(ctx, tx, fixtureAction(2, v.Token)); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if _, err := readAction(ctx, tx, 2); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("reservation survived rollback")
		}
		return insertAction(ctx, tx, fixtureAction(2, v.Token))
	})
	// No delivery rows exist: action identities are intentionally independent.
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, err := readAction(ctx, tx, 1)
		if err != nil {
			return err
		}
		got.PublicPurged = true
		got.NextCheckAt = time.Time{}
		if err = updateAction(ctx, tx, got); err != nil {
			return err
		}
		if err = insertAction(ctx, tx, a); err == nil {
			t.Fatal("replay recreated purged delivery")
		}
		outstanding, err := actionsForView(ctx, tx, v.Token)
		if err != nil {
			return err
		}
		if len(outstanding) != 1 || outstanding[0].UpdateID != 2 {
			t.Fatal("view refresh included finished tombstone or lost reservation")
		}
		return nil
	})
}
func TestStoreBindingsFencesAndIndependentFeedbackRetention(t *testing.T) {
	db := sharingTestDB(t)
	v := fixtureView()
	a := fixtureAction(1, v.Token)
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := insertView(ctx, tx, v); err != nil {
			return err
		}
		return insertAction(ctx, tx, a)
	})
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		got, err := readView(ctx, tx, v.Token)
		if err != nil {
			return err
		}
		got.BoundMessageID = 5
		if err = updateView(ctx, tx, got); err != nil {
			return err
		}
		if !errors.Is(updateView(ctx, tx, got), ErrStoreConflict) {
			t.Fatal("stale view write accepted")
		}
		got.Revision++
		got.BoundMessageID = 6
		if !errors.Is(updateView(ctx, tx, got), ErrStoreConflict) {
			t.Fatal("rebound source")
		}
		work, err := readAction(ctx, tx, 1)
		if err != nil {
			return err
		}
		work.PublicState = "unknown"
		work.PublicOutcomeAt = time.Unix(103, 0).UTC()
		work.PublicPurgeAfter = work.PublicOutcomeAt.Add(24 * time.Hour)
		work.FeedbackState = "queued"
		work.FeedbackDeliveryID = "feedback"
		work.CallbackQueryID = ""
		work.CallbackDeadline = time.Time{}
		if err = updateAction(ctx, tx, work); err != nil {
			return err
		}
		if !errors.Is(updateAction(ctx, tx, work), ErrStoreConflict) {
			t.Fatal("stale action write accepted")
		}
		work, err = readAction(ctx, tx, 1)
		if err != nil {
			return err
		}
		work.PublicPurged = true
		work.FeedbackState = "unknown"
		work.FeedbackOutcomeAt = time.Unix(110, 0).UTC()
		work.FeedbackPurgeAfter = work.FeedbackOutcomeAt.Add(24 * time.Hour)
		if err = updateAction(ctx, tx, work); err != nil {
			return err
		}
		work, err = readAction(ctx, tx, 1)
		if err != nil {
			return err
		}
		if work.FeedbackPurged || !work.PublicPurged || !work.FeedbackPurgeAfter.After(work.PublicPurgeAfter) {
			t.Fatal("retention states conflated")
		}
		work.PublicOutcomeAt = work.PublicOutcomeAt.Add(time.Second)
		if !errors.Is(updateAction(ctx, tx, work), ErrStoreConflict) {
			t.Fatal("terminal timestamp renewed")
		}
		return nil
	})
}
func TestStoreDueWorkFairBoundedAndRetirement(t *testing.T) {
	db := sharingTestDB(t)
	now := time.Unix(300, 0).UTC()
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		for i := range 105 {
			v := fixtureView()
			v.Token = strings.Repeat("a", 28) + time.Unix(int64(i), 0).Format("0405")
			if err := insertView(ctx, tx, v); err != nil {
				return err
			}
			if err := insertAction(ctx, tx, fixtureAction(runtimecontract.UpdateID(i), v.Token)); err != nil {
				return err
			}
		}
		due, err := dueActions(ctx, tx, now, 1000)
		if err != nil {
			return err
		}
		if len(due) != 100 {
			t.Fatalf("batch size %d", len(due))
		}
		first := due[0]
		first.NextCheckAt = now.Add(time.Second)
		if err = updateAction(ctx, tx, first); err != nil {
			return err
		}
		due, err = dueActions(ctx, tx, now, 1)
		if err != nil {
			return err
		}
		if due[0].UpdateID == first.UpdateID {
			t.Fatal("pending work starved later outcomes")
		}
		return nil
	})
}

func TestStoreRetirementWaitsForProjectionAndKeepsCharacters(t *testing.T) {
	db := sharingTestDB(t)
	now := time.Unix(300, 0).UTC()
	v := fixtureView()
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := insertView(ctx, tx, v); err != nil {
			return err
		}
		character := v
		character.Token = strings.Repeat("b", 32)
		character.Kind = contract.CharacterSummary
		character.UsableUntil = time.Time{}
		if err := insertView(ctx, tx, character); err != nil {
			return err
		}
		if err := insertAction(ctx, tx, fixtureAction(1, v.Token)); err != nil {
			return err
		}
		if err := retireExpiredViews(ctx, tx, now, 100); err != nil {
			return err
		}
		got, err := readView(ctx, tx, v.Token)
		if err != nil {
			return err
		}
		if got.Retired {
			t.Fatal("retired unresolved view")
		}
		a, err := readAction(ctx, tx, 1)
		if err != nil {
			return err
		}
		a.PublicState = "failed"
		a.PublicOutcomeAt = now
		a.PublicPurgeAfter = now.Add(24 * time.Hour)
		a.FeedbackState = "skipped"
		a.CallbackQueryID = ""
		a.CallbackDeadline = time.Time{}
		if err = updateAction(ctx, tx, a); err != nil {
			return err
		}
		if err = retireExpiredViews(ctx, tx, now, 100); err != nil {
			return err
		}
		got, err = readView(ctx, tx, v.Token)
		if err != nil {
			return err
		}
		if !got.Retired || got.BodyHash != nil || got.Owner != v.Owner {
			t.Fatal("retirement lost metadata or kept fingerprint")
		}
		c, err := readView(ctx, tx, character.Token)
		if err != nil {
			return err
		}
		if c.Retired || len(c.BodyHash) != 32 {
			t.Fatal("character capability expired")
		}
		if _, err = readAction(ctx, tx, 1); err != nil {
			return err
		}
		return retireExpiredViews(ctx, tx, now, 100)
	})
}

func TestStoreKeepsAuthorizationDeadlineAndDiagnosticsPrivate(t *testing.T) {
	db := sharingTestDB(t)
	v := fixtureView()
	mustTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := insertView(ctx, tx, v); err != nil {
			return err
		}
		a := fixtureAction(1, v.Token)
		if err := insertAction(ctx, tx, a); err != nil {
			return err
		}
		stored, err := readAction(ctx, tx, 1)
		if err != nil {
			return err
		}
		stored.CallbackDeadline = stored.CallbackDeadline.Add(time.Second)
		if !errors.Is(updateAction(ctx, tx, stored), ErrStoreConflict) {
			t.Fatal("callback authorization renewed")
		}
		err = insertAction(ctx, tx, a)
		if err == nil {
			t.Fatal("duplicate accepted")
		}
		for _, private := range []string{v.Token, "callback", string(a.PublicDeliveryID), "INSERT", "constraint"} {
			if strings.Contains(err.Error(), private) {
				t.Fatal("unsafe storage diagnostic")
			}
		}
		return nil
	})
	if !errors.Is(storeFailure("read", context.Canceled), context.Canceled) {
		t.Fatal("cancellation lost")
	}
}
