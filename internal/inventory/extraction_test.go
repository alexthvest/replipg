package inventory

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/alexthvest/replipg/internal/inventory/contract"
	itemcontract "github.com/alexthvest/replipg/internal/item/contract"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestInjectedContentIsValidatedAndCopied(t *testing.T) {
	db, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	starter := catalog.Starter()
	service, err := NewService(catalog, starter, chars, clock.Now)
	if err != nil {
		t.Fatal(err)
	}

	original := catalog.Items()
	catalog.items[0].ID = "changed"
	catalog.byID["healing_potion"] = itemcontract.Definition{}
	starter[0].Quantity = 999

	if !reflect.DeepEqual(service.catalog.Items(), original) {
		t.Fatal("catalog borrowed caller data")
	}

	effect, err := service.Effect("healing_potion")
	if err != nil || effect.HealingHP != 10 {
		t.Fatalf("effect changed: %+v %v", effect, err)
	}

	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		opened, err := service.Open(ctx, tx, 42)
		if err != nil {
			return err
		}

		for _, v := range opened.Current.Items {
			if v.ItemID == "coin" && v.Quantity != 10 {
				t.Fatal("starter borrowed caller data")
			}
		}
		return nil
	})
	for _, id := range []itemcontract.ItemID{"missing", "invalid id", "coin"} {
		_, err := service.Effect(id)
		var domain *contract.DomainError

		kind := contract.ErrInvalidInput
		if id == "coin" {
			kind = contract.ErrNotUsable
		}
		if !errors.Is(err, kind) || !errors.As(err, &domain) {
			t.Fatalf("untranslated error %q: %v", id, err)
		}
	}
}

func TestInjectedInvalidCatalogAndStarter(t *testing.T) {
	_, catalog, clock, _ := inventoryFixture(t)
	chars := inventoryCharacters(t, clock.Now)
	for _, port := range []Catalog{nil, (*fixtureCatalog)(nil)} {
		if _, err := NewService(port, catalog.Starter(), chars, clock.Now); !errors.Is(err, contract.ErrInvalidInput) {
			t.Fatalf("nil port: %v", err)
		}

		if _, err := LoadStarter(port); !errors.Is(err, contract.ErrInvalidCatalog) {
			t.Fatalf("nil starter port: %v", err)
		}
	}

	for _, change := range []func(*fixtureCatalog){
		func(c *fixtureCatalog) { c.items[0].ID = "bad id" },
		func(c *fixtureCatalog) { c.items[0].AttackBonus = -1 },
		func(c *fixtureCatalog) { c.items = append(c.items, c.items[0]) },
		func(c *fixtureCatalog) { delete(c.byID, c.items[0].ID) },
		func(c *fixtureCatalog) { d := c.byID["stick"]; d.AttackBonus++; c.byID["stick"] = d },
	} {
		c, err := LoadCatalog()
		if err != nil {
			t.Fatal(err)
		}

		change(&c)
		if _, err := NewService(c, c.Starter(), chars, clock.Now); !errors.Is(err, contract.ErrInvalidCatalog) {
			t.Fatalf("invalid catalog: %v", err)
		}
	}

	for _, lines := range [][]contract.GrantLine{nil, {{ItemID: "missing", Quantity: 1}}, {{ItemID: "coin", Quantity: 0}}, append(catalog.Starter(), catalog.Starter()[0])} {
		if _, err := NewService(catalog, lines, chars, clock.Now); !errors.Is(err, contract.ErrInvalidCatalog) {
			t.Fatalf("invalid starter: %v", err)
		}
	}
}

// Literal pre-extraction SQL and JSON deliberately bypass current canonical
// encoders. Existing ownership, equipment and receipts must survive a restart.
func TestLegacyInventoryEquipmentAndReceiptFixture(t *testing.T) {
	db, catalog, clock, dir := inventoryFixture(t)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO characters VALUES('0123456789abcdef0123456789abcdef',42,100,100,10,5,NULL,NULL,1);
 INSERT INTO inventory_catalog VALUES('coin','inert',0,0,0),('stick','weapon',1,0,0),('cloth_armor','armor',0,1,0),('healing_potion','consumable',0,0,10);
 INSERT INTO inventories VALUES('0123456789abcdef0123456789abcdef',4,1);
 INSERT INTO inventory_items VALUES('0123456789abcdef0123456789abcdef','coin',10),('0123456789abcdef0123456789abcdef','stick',1),('0123456789abcdef0123456789abcdef','cloth_armor',1),('0123456789abcdef0123456789abcdef','healing_potion',2);
 INSERT INTO inventory_equipment VALUES('0123456789abcdef0123456789abcdef','weapon','stick');
 INSERT INTO inventory_operations VALUES('legacy:grant','0123456789abcdef0123456789abcdef','grant','{"version":1,"items":[{"item_id":"coin","quantity":10}]}',2,NULL,NULL),('legacy:equip','0123456789abcdef0123456789abcdef','equipment','{"version":1,"slot":"weapon","item_id":"stick","expected_revision":2}',3,NULL,NULL),('legacy:consume','0123456789abcdef0123456789abcdef','consume','{"version":1,"item_id":"healing_potion","effect_kind":"heal_self_fixed_hp","healing_hp":25}',4,'heal_self_fixed_hp',25);`)
		return err
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db = openInventoryDB(t, dir)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	inventoryTx(t, db, func(ctx context.Context, tx runtimecontract.DBTX) error {
		if err := service.Prepare(ctx, tx); err != nil {
			return err
		}

		before, err := service.Inspect(ctx, tx, storeCharacterID)
		if err != nil {
			return err
		}

		if before.Weapon != "stick" || before.Modifiers.Attack != 1 {
			t.Fatalf("legacy equipment: %+v", before)
		}

		grant, err := service.Grant(ctx, tx, contract.GrantRequest{OperationID: "legacy:grant", CharacterID: storeCharacterID, Items: []contract.GrantLine{{ItemID: "coin", Quantity: 10}}})
		if err != nil {
			return err
		}

		equip, err := service.ChangeEquipment(ctx, tx, contract.EquipmentChange{OperationID: "legacy:equip", CharacterID: storeCharacterID, Slot: contract.SlotWeapon, ItemID: "stick", ExpectedRevision: 2})
		if err != nil {
			return err
		}

		// Full health and current 10 HP tuning must not reject a frozen 25 HP replay.
		healed, err := service.ConsumeHealing(ctx, tx, contract.ConsumeRequest{OperationID: "legacy:consume", CharacterID: storeCharacterID, ItemID: "healing_potion"})
		if err != nil {
			return err
		}

		if grant.Applied || equip.Applied || healed.Mutation.Applied || healed.HealingHP != 25 {
			t.Fatalf("legacy replay: %+v %+v %+v", grant, equip, healed)
		}

		after, err := service.Inspect(ctx, tx, storeCharacterID)
		if err != nil {
			return err
		}

		if !reflect.DeepEqual(before, after) {
			t.Fatal("legacy replay changed state")
		}

		return nil
	})
}
