package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/adapters/sqlite"
	application "github.com/alexthvest/replipg/internal/application/crafting"
	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
	domain "github.com/alexthvest/replipg/internal/modules/crafting"
	"github.com/alexthvest/replipg/internal/modules/inventory"
	"github.com/alexthvest/replipg/internal/modules/items"
	"github.com/alexthvest/replipg/internal/runtime"
	"github.com/alexthvest/replipg/internal/ui/menus"
	"github.com/yohamta/donburi"
)

type failingCraftProjectionReader struct {
	*sqlite.Database
}

func (f failingCraftProjectionReader) Entries(context.Context, core.GroupID, core.ID) ([]inventory.Entry, error) {
	return nil, errors.New("injected projection read failure")
}

func TestCraftingProjectionFailureReconstructsWithoutRepeatedMutation(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, testConfig(t).DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = db.Close()
	}()

	character, err := db.EnsureCharacter(ctx, characters.Character{
		ID:    "crafter",
		Group: -1,
		User:  2,
		Name:  "Crafter",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := items.Snapshot{
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
	}
	err = db.WithinCrafting(ctx, func(tx application.Transaction) error {
		_, err := inventory.Grant(ctx, tx.Inventory, core.NewID, -1, character.ID, snapshot, 3)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := db.Entries(ctx, -1, character.ID)
	if err != nil {
		t.Fatal(err)
	}

	service, err := application.New(db, core.NewID, time.Now, true)
	if err != nil {
		t.Fatal(err)
	}
	confirmation := application.Confirmation{
		Token: "confirmation",
		Scope: core.Scope{
			Group: -1,
			Actor: 2,
		},
		Character: character.ID,
		MessageID: 1,
		Expires:   time.Now().Add(time.Minute),
		Allocations: []domain.Allocation{{
			Entry:    entries[0].ID,
			Snapshot: snapshot,
			Quantity: 1,
		}},
	}
	attempt, err := service.Confirm(ctx, confirmation)
	if err != nil {
		t.Fatal(err)
	}
	claim, found, err := service.Claim(ctx, time.Minute)
	if err != nil || !found {
		t.Fatalf("claim %v %v", found, err)
	}
	output := snapshot
	output.English.Name = "Surreal material"
	if err = service.Complete(ctx, claim, domain.Outcome{
		Kind: domain.Success,
		Item: &output,
	}); err != nil {
		t.Fatal(err)
	}

	sessions, err := menus.New(time.Minute, 10, time.Now, menus.RandomToken)
	if err != nil {
		t.Fatal(err)
	}
	broken, err := inventory.New(failingCraftProjectionReader{Database: db})
	if err != nil {
		t.Fatal(err)
	}
	modules := &modules{
		crafting:            service,
		inventory:           broken,
		inventoryProjection: inventory.NewProjection(),
		sessions:            sessions,
	}
	world := donburi.NewWorld()
	var projectionError *runtime.ProjectionError
	if err = modules.projectCrafting(ctx, world, attempt.Recipe); !errors.As(err, &projectionError) {
		t.Fatalf("projection failure must stop serving untrusted state: %v", err)
	}

	modules.inventory, err = inventory.New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = modules.projectCrafting(ctx, world, attempt.Recipe); err != nil {
		t.Fatal(err)
	}
	projected, ok := modules.inventoryProjection.Get(world, -1, entries[0].ID)
	if !ok || projected.Quantity != 2 {
		t.Fatalf("reconstruction %+v", projected)
	}
	replay, err := service.Confirm(ctx, confirmation)
	if err != nil || replay.State != domain.Success {
		t.Fatalf("replay %+v %v", replay, err)
	}
	if err = modules.projectCrafting(ctx, world, attempt.Recipe); err != nil {
		t.Fatal(err)
	}
	projected, ok = modules.inventoryProjection.Get(world, -1, entries[0].ID)
	if !ok || projected.Quantity != 2 {
		t.Fatalf("projection retry repeated mutation: %+v", projected)
	}
}
