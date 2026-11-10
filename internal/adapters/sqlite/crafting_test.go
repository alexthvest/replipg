package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	application "github.com/alexthvest/replipg/internal/application/crafting"
	shopapp "github.com/alexthvest/replipg/internal/application/shop"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	shopdomain "github.com/alexthvest/replipg/internal/modules/shop"
)

type craftFixture struct {
	db       *Database
	service  *application.Service
	now      time.Time
	snapshot items.Snapshot
}

func (f *craftFixture) outcome() domain.Outcome {
	return domain.Outcome{
		Kind: domain.Success,
		Item: &f.snapshot,
	}
}

func newCraftFixture(t *testing.T) *craftFixture {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "craft.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	f := &craftFixture{
		db:  db,
		now: time.Unix(1000, 0),
		snapshot: items.Snapshot{
			Version:   1,
			CatalogID: "material",
			Mode:      items.Stackable,
			Rarity:    "common",
			English: items.Text{
				Name:        "Material",
				Description: "Saved",
			},
			Russian: items.Text{
				Name:        "Материал",
				Description: "Сохранён",
			},
			Components: items.Components{Material: true},
		},
	}
	f.service, err = application.New(db, core.NewID, func() time.Time {
		return f.now
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []characters.Character{
		{
			ID:    "c1",
			Group: -1,
			User:  1,
			Name:  "One",
		},
		{
			ID:    "c2",
			Group: -2,
			User:  2,
			Name:  "Two",
		},
	} {
		if _, err = db.EnsureCharacter(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		if err = db.WithinCrafting(context.Background(), func(tx application.Transaction) error {
			_, err := inventory.Grant(context.Background(), tx.Inventory, core.NewID, c.Group, c.ID, f.snapshot, 20)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *craftFixture) confirmation(t *testing.T, token core.ID, group core.GroupID, user core.UserID, character core.ID, quantity int64) application.Confirmation {
	t.Helper()
	entries, err := f.db.Entries(context.Background(), group, character)
	if err != nil || len(entries) == 0 {
		t.Fatalf("entries %v %v", entries, err)
	}
	return application.Confirmation{
		Token: token,
		Scope: core.Scope{
			Group:    group,
			Actor:    user,
			BotID:    123,
			UpdateID: 1,
		},
		Character: character,
		MessageID: 4,
		Expires:   f.now.Add(time.Minute),
		Allocations: []domain.Allocation{{
			Entry:    entries[0].ID,
			Snapshot: entries[0].Snapshot,
			Quantity: quantity,
		}},
	}
}

func TestCraftGlobalOutcomeReplayAndRestart(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	c := f.confirmation(t, "first", -1, 1, "c1", 3)
	a, err := f.service.Confirm(ctx, c)
	if err != nil || a.State != domain.Pending {
		t.Fatalf("confirm %+v %v", a, err)
	}
	claim, ok, err := f.service.Claim(ctx, time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim %v %v", ok, err)
	}
	output := f.snapshot
	output.English.Name = "Generated"
	output.CatalogID = "ignored"
	if err = f.service.Complete(ctx, claim, domain.Outcome{
		Kind: domain.Success,
		Item: &output,
	}); err != nil {
		t.Fatal(err)
	}
	a, _, err = f.service.Attempt(ctx, c.Token)
	if err != nil || a.State != domain.Success {
		t.Fatalf("result %+v %v", a, err)
	}
	if a.Outcome.Item.CatalogID == "ignored" {
		t.Fatal("model chose ID")
	}
	c.Scope.UpdateID = 2
	if _, err = f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	entries, _ := f.db.Entries(ctx, -1, "c1")
	if len(entries) != 2 || entries[0].Quantity != 17 || entries[1].Quantity != 1 {
		t.Fatalf("duplicate mutated %+v", entries)
	}
	// Reopen and use the saved global result with discovery disabled.
	var path string
	if err = f.db.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.db.Close()
	})
	f.service, err = application.New(f.db, core.NewID, func() time.Time {
		return f.now
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	c2 := f.confirmation(t, "other-group", -2, 2, "c2", 3)
	b, err := f.service.Confirm(ctx, c2)
	if err != nil || b.State != domain.Success || *b.Outcome.Item != *a.Outcome.Item {
		t.Fatalf("global reuse %+v %v", b, err)
	}
	if _, ok, err = f.service.Claim(ctx, time.Minute); err != nil || ok {
		t.Fatal("known outcome requested AI")
	}
	c.Token = "same-owner-output-stack"
	c.Expires = f.now.Add(time.Minute)
	if _, err = f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	entries, err = f.db.Entries(ctx, -1, "c1")
	if err != nil || len(entries) != 2 || entries[0].Quantity != 14 || entries[1].Quantity != 2 || entries[1].Snapshot != *a.Outcome.Item {
		t.Fatalf("complete saved output did not stack: %+v %v", entries, err)
	}
	c2.Token = "unknown"
	c2.Allocations[0].Quantity = 2
	if _, err = f.service.Confirm(ctx, c2); !errors.Is(err, application.ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
}

func TestCraftSuccessfulRepeatedAttemptsAndScope(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	c := f.confirmation(t, "first-success", -1, 1, "c1", 2)
	if _, err := f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	claim, _, _ := f.service.Claim(ctx, time.Minute)
	if err := f.service.Complete(ctx, claim, f.outcome()); err != nil {
		t.Fatal(err)
	}
	c.Token = "repeat"
	a, err := f.service.Confirm(ctx, c)
	if err != nil || a.State != domain.Success {
		t.Fatalf("repeat %+v %v", a, err)
	}
	entries, _ := f.db.Entries(ctx, -1, "c1")
	if len(entries) != 2 || entries[0].Quantity != 16 || entries[1].Quantity != 2 {
		t.Fatalf("successes did not consume and grant exactly once: %+v", entries)
	}
	c.Scope.Actor = 2
	if _, err = f.service.Confirm(ctx, c); !errors.Is(err, application.ErrConfirmation) {
		t.Fatalf("foreign replay %v", err)
	}
	c.Scope.Actor = 1
	c.Token = "expired"
	c.Expires = f.now
	if _, err = f.service.Confirm(ctx, c); !errors.Is(err, application.ErrConfirmation) {
		t.Fatalf("expired %v", err)
	}
}

func TestCraftClaimRecoveryFailureAndExplicitRetry(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	c := f.confirmation(t, "interrupted", -1, 1, "c1", 2)
	if _, err := f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	old, _, _ := f.service.Claim(ctx, time.Minute)
	f.now = f.now.Add(2 * time.Minute)
	current, ok, err := f.service.Claim(ctx, time.Minute)
	if err != nil || !ok || old.Token == current.Token {
		t.Fatal("claim not recovered")
	}
	if err = f.service.Complete(ctx, old, f.outcome()); err != nil {
		t.Fatal(err)
	}
	a, _, _ := f.service.Attempt(ctx, c.Token)
	if a.State != domain.Pending {
		t.Fatal("stale result accepted")
	}
	if err = f.service.Fail(ctx, current); err != nil {
		t.Fatal(err)
	}
	a, _, _ = f.service.Attempt(ctx, c.Token)
	if a.State != domain.TechnicalFailure {
		t.Fatalf("failure %+v", a)
	}
	if _, ok, _ = f.service.Claim(ctx, time.Minute); ok {
		t.Fatal("automatic failure retry")
	}
	entries, _ := f.db.Entries(ctx, -1, "c1")
	if entries[0].Quantity != 20 {
		t.Fatal("technical failure consumed")
	}
	c.Token = "intentional-retry"
	c.Expires = f.now.Add(time.Minute)
	if _, err = f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = f.service.Claim(ctx, time.Minute); err != nil || !ok {
		t.Fatal("explicit retry unavailable")
	}
}

func TestCraftConcurrentConfirmationAndUnavailableFulfillment(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	c := f.confirmation(t, "concurrent", -1, 1, "c1", 19)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.service.Confirm(ctx, c); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var count int
	if err := f.db.db.QueryRow("SELECT count(*) FROM crafting_attempts").Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate attempts %d %v", count, err)
	}
	claim, _, _ := f.service.Claim(ctx, time.Minute)
	if _, err := f.db.db.Exec("UPDATE inventory_entries SET quantity=18 WHERE id=?", c.Allocations[0].Entry); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Complete(ctx, claim, f.outcome()); err != nil {
		t.Fatal(err)
	}
	a, _, _ := f.service.Attempt(ctx, c.Token)
	if a.State != domain.Unavailable {
		t.Fatalf("unavailable %+v", a)
	}
	entries, _ := f.db.Entries(ctx, -1, "c1")
	if entries[0].Quantity != 18 {
		t.Fatal("unavailable consumed")
	}
	if o, found, err := f.service.Lookup(ctx, c.Allocations); err != nil || !found || o.Kind != domain.Success {
		t.Fatal("global outcome lost")
	}
}

func TestCraftOutputFailureRollsBackAndRecoversSavedOutcome(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	c := f.confirmation(t, "rollback", -1, 1, "c1", 20)
	if _, err := f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	claim, _, _ := f.service.Claim(ctx, time.Minute)
	if _, err := f.db.db.Exec("CREATE TRIGGER fail_craft_output BEFORE INSERT ON inventory_entries BEGIN SELECT RAISE(ABORT,'injected output failure'); END"); err != nil {
		t.Fatal(err)
	}
	output := f.snapshot
	output.English.Name = "Output"
	if err := f.service.Complete(ctx, claim, domain.Outcome{
		Kind: domain.Success,
		Item: &output,
	}); err == nil {
		t.Fatal("injected failure ignored")
	}
	a, _, _ := f.service.Attempt(ctx, c.Token)
	if a.State != domain.Pending {
		t.Fatal("terminal result committed with failed output")
	}
	entries, _ := f.db.Entries(ctx, -1, "c1")
	if len(entries) != 1 || entries[0].Quantity != 20 {
		t.Fatal("partial consumption")
	}
	if _, err := f.db.db.Exec("DROP TRIGGER fail_craft_output"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	entries, _ = f.db.Entries(ctx, -1, "c1")
	if len(entries) != 1 || entries[0].Quantity != 1 || entries[0].Snapshot.English.Name != "Output" {
		t.Fatalf("recover %+v", entries)
	}
}

func TestCraftPreservesShopReservationsAndRejectsDuplicateAllocations(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	c := f.confirmation(t, "reserved", -1, 1, "c1", 15)
	order := testOrder(f.snapshot, "c1", "sale", shopdomain.Sell)
	order.Quote.Player = 1
	order.Quote.Entry = c.Allocations[0].Entry
	order.Quote.Quantity = 5
	order.Quote.Total = "50"
	if err := f.db.WithinShop(ctx, func(tx shopapp.Transaction) error {
		if err := createOrder(ctx, tx, order, 100); err != nil {
			return err
		}
		return tx.Reservations.Reserve(ctx, order)
	}); err != nil {
		t.Fatal(err)
	}
	invalid := c
	invalid.Token = "too-many"
	invalid.Allocations = append([]domain.Allocation(nil), c.Allocations...)
	invalid.Allocations[0].Quantity = 16
	if _, err := f.service.Confirm(ctx, invalid); !errors.Is(err, domain.ErrUnavailable) {
		t.Fatalf("accepted reserved units: %v", err)
	}
	invalid.Token = "duplicate"
	invalid.Allocations[0].Quantity = 1
	invalid.Allocations = append(invalid.Allocations, invalid.Allocations[0])
	if _, err := f.service.Confirm(ctx, invalid); !errors.Is(err, domain.ErrIngredients) {
		t.Fatalf("accepted duplicate allocation: %v", err)
	}
	if _, err := f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	claim, _, _ := f.service.Claim(ctx, time.Minute)
	if err := f.service.Complete(ctx, claim, f.outcome()); err != nil {
		t.Fatal(err)
	}
	entries, err := f.db.Entries(ctx, -1, "c1")
	if err != nil || len(entries) != 2 || entries[0].Quantity != 5 || entries[0].Reserved != 5 || entries[0].ID != c.Allocations[0].Entry || entries[1].Quantity != 1 {
		t.Fatalf("reservation damaged: %+v %v", entries, err)
	}
	var reserved int
	if err = f.db.db.QueryRow("SELECT quantity FROM inventory_reservations WHERE order_id='sale'").Scan(&reserved); err != nil || reserved != 5 {
		t.Fatalf("reservation lost: %d %v", reserved, err)
	}
}

func TestCraftManyTypesIndividualAndRecursiveMaterials(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	if err := f.db.WithinCrafting(ctx, func(tx application.Transaction) error {
		for _, id := range []string{"one", "two", "three"} {
			snapshot := f.snapshot
			snapshot.CatalogID = id
			snapshot.Mode = items.Individual
			if _, err := inventory.Grant(ctx, tx.Inventory, core.NewID, -1, "c1", snapshot, 2); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	entries, _ := f.db.Entries(ctx, -1, "c1")
	c := f.confirmation(t, "many", -1, 1, "c1", 1)
	for _, e := range entries[1:] {
		c.Allocations = append(c.Allocations, domain.Allocation{
			Entry:    e.ID,
			Snapshot: e.Snapshot,
			Quantity: 1,
		})
	}
	if _, err := f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	claim, _, _ := f.service.Claim(ctx, time.Minute)
	if len(claim.Ingredients) != 4 {
		t.Fatalf("ingredient type restriction: %+v", claim.Ingredients)
	}
	output := f.snapshot
	output.English.Name = "Recursive material"
	if err := f.service.Complete(ctx, claim, domain.Outcome{
		Kind: domain.Success,
		Item: &output,
	}); err != nil {
		t.Fatal(err)
	}
	entries, _ = f.db.Entries(ctx, -1, "c1")
	if len(entries) != 2 {
		t.Fatalf("individual inputs remain: %+v", entries)
	}
	generated := entries[1]
	c.Token = "recursive"
	c.Allocations = []domain.Allocation{{
		Entry:    generated.ID,
		Snapshot: generated.Snapshot,
		Quantity: 1,
	}}
	if _, err := f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	claim, _, _ = f.service.Claim(ctx, time.Minute)
	if err := f.service.Complete(ctx, claim, f.outcome()); err != nil {
		t.Fatal(err)
	}
	entries, _ = f.db.Entries(ctx, -1, "c1")
	if len(entries) != 2 || entries[0].Quantity != 19 || entries[1].ID == generated.ID || entries[1].Quantity != 1 {
		t.Fatalf("recursive output not consumed: %+v", entries)
	}
}

func TestCraftCanonicalCollisionLookup(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	first, _ := domain.Identify([]domain.Allocation{{
		Snapshot: f.snapshot,
		Quantity: 1,
	}})
	second, _ := domain.Identify([]domain.Allocation{{
		Snapshot: f.snapshot,
		Quantity: 2,
	}})
	second.Digest = first.Digest
	if err := f.db.WithinCrafting(ctx, func(tx application.Transaction) error {
		for _, r := range []application.Recipe{
			{
				ID:       "first",
				Identity: first,
			},
			{
				ID:       "second",
				Identity: second,
			},
		} {
			if _, err := tx.Repository.EnsureRecipe(ctx, r); err != nil {
				return err
			}
		}
		for _, test := range []struct {
			identity domain.Identity
			id       core.ID
		}{
			{
				identity: first,
				id:       "first",
			},
			{
				identity: second,
				id:       "second",
			},
		} {
			r, found, err := tx.Repository.Recipe(ctx, test.identity)
			if err != nil || !found || r.ID != test.id {
				t.Fatalf("collision conflated identities: %+v %v", r, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCraftMigrationPreservesVersionFourOwnershipAndReservations(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	c := f.confirmation(t, "unused", -1, 1, "c1", 1)
	order := testOrder(f.snapshot, "c1", "historical-sale", shopdomain.Sell)
	order.Quote.Player = 1
	order.Quote.Entry = c.Allocations[0].Entry
	if err := f.db.WithinShop(ctx, func(tx shopapp.Transaction) error {
		if err := createOrder(ctx, tx, order, 100); err != nil {
			return err
		}
		return tx.Reservations.Reserve(ctx, order)
	}); err != nil {
		t.Fatal(err)
	}
	before, err := f.db.LoadInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"DROP TABLE crafting_jobs",
		"DROP TABLE crafting_attempts",
		"DROP TABLE crafting_recipes",
		"PRAGMA user_version=4",
	} {
		if _, err = f.db.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var path string
	if err = f.db.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.db.Close()
	})
	after, err := f.db.LoadInventory(ctx)
	if err != nil || len(after) != len(before) {
		t.Fatalf("migration ownership %+v %v", after, err)
	}
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("migration rewrote saved ownership: %+v %+v", before[i], after[i])
		}
	}
	if err = f.db.WithinShop(ctx, func(tx shopapp.Transaction) error {
		saved, found, err := tx.Orders.Order(ctx, order.ID)
		if err != nil || !found || saved != order {
			t.Fatalf("migration shop quote %+v %v", saved, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCraftSimultaneousDiscoveryConvergesAndLateOutcomeCannotChangeWinner(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	confirmations := []application.Confirmation{
		f.confirmation(t, "group-one", -1, 1, "c1", 2),
		f.confirmation(t, "group-two", -2, 2, "c2", 2),
	}
	var wg sync.WaitGroup
	for _, c := range confirmations {
		wg.Go(func() {
			if _, err := f.service.Confirm(ctx, c); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	claim, found, err := f.service.Claim(ctx, time.Minute)
	if err != nil || !found {
		t.Fatalf("discovery job %v %v", found, err)
	}
	if _, found, err = f.service.Claim(ctx, time.Minute); err != nil || found {
		t.Fatalf("duplicate active job %v %v", found, err)
	}
	if err = f.service.Complete(ctx, claim, f.outcome()); err != nil {
		t.Fatal(err)
	}
	output := f.snapshot
	output.English.Name = "Conflicting late output"
	if err = f.service.Complete(ctx, claim, domain.Outcome{
		Kind: domain.Success,
		Item: &output,
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range confirmations {
		a, found, err := f.service.Attempt(ctx, c.Token)
		if err != nil || !found || a.State != domain.Success || a.Outcome.Kind != domain.Success {
			t.Fatalf("conflicting result %+v %v", a, err)
		}
		entries, err := f.db.Entries(ctx, c.Scope.Group, c.Character)
		if err != nil || len(entries) != 2 || entries[0].Quantity != 18 || entries[1].Quantity != 1 || entries[1].Snapshot.English.Name == output.English.Name {
			t.Fatalf("repeated mutation %+v %v", entries, err)
		}
	}
}

func TestCraftRejectionIsTechnicalFailureWithoutConsumptionOrCachedOutcome(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	c := f.confirmation(t, "invalid-rejection", -1, 1, "c1", 2)
	if _, err := f.service.Confirm(ctx, c); err != nil {
		t.Fatal(err)
	}
	claim, _, _ := f.service.Claim(ctx, time.Minute)
	rejection := domain.Outcome{Kind: domain.Uncraftable}
	if err := f.db.WithinCrafting(ctx, func(tx application.Transaction) error {
		_, err := tx.Repository.Publish(ctx, claim, rejection)
		return err
	}); err == nil {
		t.Fatal("repository accepted a new rejected recipe")
	}
	if err := f.service.Complete(ctx, claim, rejection); err == nil {
		t.Fatal("service accepted gameplay rejection")
	}
	a, found, err := f.service.Attempt(ctx, c.Token)
	if err != nil || !found || a.State != domain.TechnicalFailure {
		t.Fatalf("invalid response did not fail technically: %+v %v", a, err)
	}
	entries, err := f.db.Entries(ctx, -1, "c1")
	if err != nil || len(entries) != 1 || entries[0].Quantity != 20 {
		t.Fatalf("invalid response consumed ingredients: %+v %v", entries, err)
	}
	if _, found, err = f.service.Lookup(ctx, c.Allocations); err != nil || found {
		t.Fatalf("invalid response was cached: %v %v", found, err)
	}
}

func TestCraftSuccessOnlyMigrationPreservesHistoryAndRediscoversRejectedRecipes(t *testing.T) {
	f := newCraftFixture(t)
	ctx := context.Background()
	c := f.confirmation(t, "historical-rejection", -1, 1, "c1", 2)
	historical, err := f.service.Confirm(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	claim, _, _ := f.service.Claim(ctx, time.Minute)

	// Recreate version-five policy and its already committed destructive result.
	for _, name := range []string{
		"crafting_success_insert",
		"crafting_success_update",
		"crafting_success_attempt_insert",
		"crafting_success_attempt_update",
	} {
		if _, err = f.db.db.Exec("DROP TRIGGER " + name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.db.db.Exec("UPDATE crafting_recipes SET outcome=? WHERE id=?", []byte(`{"kind":"uncraftable"}`), claim.Recipe); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.db.Exec("UPDATE crafting_jobs SET state='done' WHERE recipe_id=?", claim.Recipe); err != nil {
		t.Fatal(err)
	}
	if err = f.db.WithinCrafting(ctx, func(tx application.Transaction) error {
		if err := tx.Repository.Consume(ctx, historical); err != nil {
			return err
		}
		historical.State = domain.Uncraftable
		historical.Outcome = domain.Outcome{Kind: domain.Uncraftable}
		return tx.Repository.SaveAttempt(ctx, historical)
	}); err != nil {
		t.Fatal(err)
	}

	known := f.confirmation(t, "preserved-success", -2, 2, "c2", 3)
	if _, err = f.service.Confirm(ctx, known); err != nil {
		t.Fatal(err)
	}
	successClaim, _, _ := f.service.Claim(ctx, time.Minute)
	if err = f.service.Complete(ctx, successClaim, f.outcome()); err != nil {
		t.Fatal(err)
	}
	success, _, err := f.service.Lookup(ctx, known.Allocations)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.db.LoadInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var historyBefore []byte
	if err = f.db.db.QueryRow("SELECT result FROM crafting_attempts WHERE confirmation=?", c.Token).Scan(&historyBefore); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.db.Exec("PRAGMA user_version=5"); err != nil {
		t.Fatal(err)
	}
	var path string
	if err = f.db.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Close(); err != nil {
		t.Fatal(err)
	}
	f.db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.db.Close()
	})
	f.service, err = application.New(f.db, core.NewID, func() time.Time {
		return f.now
	}, true)
	if err != nil {
		t.Fatal(err)
	}

	after, err := f.db.LoadInventory(ctx)
	if err != nil || len(after) != len(before) {
		t.Fatalf("migration changed inventory: %+v %v", after, err)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("migration rewrote ownership: %+v %+v", before[i], after[i])
		}
	}
	var historyAfter []byte
	if err = f.db.db.QueryRow("SELECT result FROM crafting_attempts WHERE confirmation=?", c.Token).Scan(&historyAfter); err != nil || string(historyAfter) != string(historyBefore) {
		t.Fatalf("migration changed historical result: %s %v", historyAfter, err)
	}
	if old, err := f.service.Confirm(ctx, c); err != nil || old.State != domain.Uncraftable || old.ID != historical.ID {
		t.Fatalf("historical replay changed: %+v %v", old, err)
	}
	if _, found, err := f.service.Lookup(ctx, c.Allocations); err != nil || found {
		t.Fatalf("old rejection remains authoritative: %v %v", found, err)
	}
	preserved, found, err := f.service.Lookup(ctx, known.Allocations)
	if err != nil || !found || preserved.Kind != domain.Success || *preserved.Item != *success.Item {
		t.Fatalf("successful recipe changed: %+v %v", preserved, err)
	}

	c.Token = "rediscovered"
	if a, err := f.service.Confirm(ctx, c); err != nil || a.State != domain.Pending {
		t.Fatalf("old rejection did not permit discovery: %+v %v", a, err)
	}
	claim, found, err = f.service.Claim(ctx, time.Minute)
	if err != nil || !found || claim.Recipe != historical.Recipe {
		t.Fatalf("missing rediscovery job: %+v %v", claim, err)
	}
	if err = f.service.Complete(ctx, claim, f.outcome()); err != nil {
		t.Fatal(err)
	}
	entries, err := f.db.Entries(ctx, -1, "c1")
	if err != nil || len(entries) != 2 || entries[0].Quantity != 16 || entries[1].Quantity != 1 {
		t.Fatalf("rediscovery effects: %+v %v", entries, err)
	}
}
