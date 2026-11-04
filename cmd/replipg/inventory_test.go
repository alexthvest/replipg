package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/inventory/contract"
	"github.com/alexthvest/replipg/internal/persistence"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestInventoryProductionPreparation(t *testing.T) {
	options, err := productionOptions(config.Config{}, commandLookup(nil))
	if err != nil {
		t.Fatal(err)
	}

	db, err := persistence.Open(context.Background(), persistence.Options{StorageDir: t.TempDir(), StorageFilesystem: config.FilesystemLocal, BusyTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	migrations, err := persistence.RuntimeMigrations()
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(context.Background(), migrations); err != nil {
		t.Fatal(err)
	}

	if options.Prepare == nil {
		t.Fatal("no catalog preparation")
	}

	if err := db.WithinTransaction(context.Background(), options.Prepare); err != nil {
		t.Fatal(err)
	}

	var roots, definitions int

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM inventories`).Scan(&roots); err != nil {
		t.Fatal(err)
	}

	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM inventory_catalog`).Scan(&definitions); err != nil {
		t.Fatal(err)
	}

	if roots != 0 || definitions < 4 {
		t.Fatal("preparation failed to reconcile or granted items")
	}

	if err := db.WithinTransaction(context.Background(), func(ctx context.Context, tx runtimecontract.DBTX) error {
		_, err := tx.ExecContext(ctx, `UPDATE inventory_catalog SET healing_hp=25 WHERE item_id='healing_potion'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.WithinTransaction(context.Background(), options.Prepare); !errors.Is(err, contract.ErrInvalidCatalog) {
		t.Fatalf("incompatible saved catalog accepted %v", err)
	}
}

func TestInventoryStrictDevelopmentFlagAndIndependentModes(t *testing.T) {
	for _, value := range []string{"true", "false", "TRUE", "1", "", "true ", "private-value"} {
		t.Run(value, func(t *testing.T) {
			options, err := productionOptions(config.Config{}, commandLookup(map[string]string{envDevInventory: value, envOwnerTelegramUserID: "7"}))

			if value != "true" && value != "false" {
				if err == nil || strings.Contains(err.Error(), "private-value") {
					t.Fatal("invalid flag accepted/leaked")
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			registry := interaction.NewRegistry()
			if err := options.Register(sharingTestRegistrations(t, registry)); err != nil {
				t.Fatal(err)
			}

			registry.Seal()
			enabled := value == "true"
			for _, name := range []interaction.CommandName{"dev_item", "dev_inventory_grant", "dev_inventory_heal"} {
				if _, ok := registry.Resolve(interaction.Command{Identity: interaction.Identity{Update: 1, User: 42, Chat: -1, ChatKind: runtimecontract.ChatKindGroup}, Name: name}); ok != enabled {
					t.Fatal("development route mismatch")
				}
			}

			want := 3
			if enabled {
				want = 6
			}

			if len(options.Commands) != want {
				t.Fatal("menu mismatch")
			}

			for _, declaration := range options.Commands {
				if !declaration.Ephemeral {
					t.Fatal("public probe declaration")
				}
			}
		})
	}

	for _, flags := range []map[string]string{nil, {envDevInventory: "true", envOwnerTelegramUserID: "7", envDevCharacter: "true"}, {envDevInventory: "false", envDevCharacter: "true"}, {envDevInventory: "true", envOwnerTelegramUserID: "7", envDevCharacter: "false"}} {
		options, err := productionOptions(config.Config{DevSmoke: true}, commandLookup(flags))
		if err != nil {
			t.Fatal(err)
		}

		registry := interaction.NewRegistry()
		if err := options.Register(sharingTestRegistrations(t, registry)); err != nil {
			t.Fatal(err)
		}

		want := 5
		if flags[envDevInventory] == "true" {
			want += 3
		}
		if flags[envDevCharacter] == "true" {
			want += 2
		}

		if len(options.Commands) != want {
			t.Fatal("independent menu flags")
		}
	}
}

func TestInventoryDevelopmentRequiresStrictOwner(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "+7", " 7", "7 ", "9223372036854775808", "secret-owner"} {
		t.Run(value, func(t *testing.T) {
			_, err := productionOptions(config.Config{}, commandLookup(map[string]string{envDevInventory: "true", "REPLIPG_OWNER_TELEGRAM_USER_ID": value}))
			if err == nil {
				t.Fatal("invalid owner accepted")
			}

			if value == "secret-owner" && strings.Contains(err.Error(), value) {
				t.Fatal("owner leaked")
			}
		})
	}

	if _, err := productionOptions(config.Config{}, commandLookup(map[string]string{envDevInventory: "true"})); err == nil {
		t.Fatal("missing owner accepted")
	}

	if _, err := productionOptions(config.Config{}, commandLookup(map[string]string{envDevInventory: "false", "REPLIPG_OWNER_TELEGRAM_USER_ID": "invalid"})); err != nil {
		t.Fatal(err)
	}
}

func TestInventoryDevelopmentOwnerAcceptsPositiveInt64(t *testing.T) {
	for _, value := range []string{"1", "7", "0007", "9223372036854775807"} {
		if _, err := productionOptions(config.Config{}, commandLookup(map[string]string{envDevInventory: "true", envOwnerTelegramUserID: value})); err != nil {
			t.Fatalf("valid owner %s: %v", value, err)
		}
	}
}
