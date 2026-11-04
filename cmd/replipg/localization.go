package main

import (
	"context"
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/devsmoke"
	"github.com/alexthvest/replipg/internal/inventory"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/settings"
	"github.com/alexthvest/replipg/internal/sharing"
)

const envDevCharacter = "REPLIPG_DEV_CHARACTER"

// productionOptions validates all presentation resources before constructing the
// runtime. The same ordered command specifications drive global and chat menus.
func productionOptions(cfg config.Config, lookup config.LookupEnv) (app.Options, error) {
	inventoryDevelopment, err := inventoryDevelopmentMode(lookup)
	if err != nil {
		return app.Options{}, err
	}
	development := false
	if lookup != nil {
		if value, present := lookup(envDevCharacter); present {
			switch value {
			case "true":
				development = true
			case "false":
			default:
				return app.Options{}, errors.New("REPLIPG_DEV_CHARACTER: must be true or false")
			}
		}
	}
	tuning, err := character.LoadConfig(development)
	if err != nil {
		return app.Options{}, err
	}

	characters, err := character.NewService(tuning, time.Now)
	if err != nil {
		return app.Options{}, err
	}
	items, err := inventory.LoadCatalog()
	if err != nil {
		return app.Options{}, err
	}
	supplies, err := inventory.NewService(items, characters, time.Now)
	if err != nil {
		return app.Options{}, err
	}
	catalog, err := localization.Load()
	if err != nil {
		return app.Options{}, err
	}

	ids := make([]string, 0, len(items.Items()))
	for _, item := range items.Items() {
		ids = append(ids, string(item.ID))
	}

	if err := catalog.ValidateItems(ids, inventory.ValidateItemText); err != nil {
		return app.Options{}, err
	}

	shares, err := sharing.NewService(catalog, sharing.ServiceOptions{Now: time.Now})
	if err != nil {
		return app.Options{}, err
	}

	specs := []settings.CommandSpec{{Name: "character", Description: localization.Key("command.character"), Ephemeral: true}}
	if development {
		specs = append(specs, settings.CommandSpec{Name: "dev_character_damage", Description: localization.Key("command.dev_character_damage"), Ephemeral: true}, settings.CommandSpec{Name: "dev_character_defeat", Description: localization.Key("command.dev_character_defeat"), Ephemeral: true})
	}
	specs = append(specs, settings.CommandSpec{Name: "inventory", Description: localization.Key("command.inventory"), Ephemeral: true})
	if inventoryDevelopment {
		specs = append(specs, settings.CommandSpec{Name: "dev_inventory_grant", Description: localization.Key("command.dev_inventory_grant"), Ephemeral: true}, settings.CommandSpec{Name: "dev_inventory_heal", Description: localization.Key("command.dev_inventory_heal"), Ephemeral: true})
	}
	specs = append(specs, settings.CommandSpec{Name: "settings", Description: localization.Key("command.settings"), Ephemeral: true})
	if cfg.DevSmoke {
		specs = append(specs, settings.CommandSpec{Name: "dev_smoke", Description: localization.Key("command.dev_smoke"), Ephemeral: false}, settings.CommandSpec{Name: "dev_smoke_ephemeral", Description: localization.Key("command.dev_smoke_ephemeral"), Ephemeral: true})
	}
	builder, err := settings.NewMenuBuilder(catalog, specs)
	if err != nil {
		return app.Options{}, err
	}

	store := settings.NewStore(builder)
	commands, err := builder.Commands(localization.English)
	if err != nil {
		return app.Options{}, err
	}

	var ledger *sharingLedger
	var projector *sharing.Projector

	// Runtime finishes registration and worker construction before admitting
	// updates. Bind refresh through that existing ordering without reordering recovery.
	refresh := func(ctx context.Context, tx runtimecontract.DBTX, token string) error {
		if projector == nil {
			return errors.New("sharing: projector not initialized")
		}

		return projector.Refresh(ctx, tx, token)
	}
	return app.Options{
		Commands: commands,
		Prepare: func(ctx context.Context, tx runtimecontract.DBTX) error {
			if err := supplies.Prepare(ctx, tx); err != nil {
				return err
			}

			return store.Prepare(ctx, tx, time.Now())
		},
		Register: func(r app.Registrations) error {
			ledger, err = newSharingLedger(r.Outbox)
			if err != nil {
				return err
			}

			if err := sharing.Register(r.Registry, shares, ledger, refresh, store.Resolve); err != nil {
				return err
			}

			characterPresentation := character.Presentation{Catalog: catalog, Resolve: store.Resolve, Sharing: shares}
			if err := character.Register(r.Registry, characters, time.Now, characterPresentation); err != nil {
				return err
			}

			if development {
				if err := character.RegisterDevelopment(r.Registry, characters, time.Now, characterPresentation); err != nil {
					return err
				}
			}
			inventoryPresentation := inventory.Presentation{Catalog: catalog, Resolve: store.Resolve, Sharing: shares}
			if err := inventory.Register(r.Registry, supplies, time.Now, inventoryPresentation); err != nil {
				return err
			}

			if inventoryDevelopment {
				if err := inventory.RegisterDevelopment(r.Registry, supplies, time.Now, inventoryPresentation); err != nil {
					return err
				}
			}
			if err := settings.Register(r.Registry, store, catalog, r.Membership, time.Now); err != nil {
				return err
			}

			if cfg.DevSmoke {
				return devsmoke.Register(r.Registry, time.Now, devsmoke.Presentation{Catalog: catalog, Resolve: store.Resolve})
			}

			return nil
		},
		Workers: []app.WorkerFactory{{Name: "group_menu", New: func(tx runtimecontract.Transactor, menus runtimecontract.GroupMenuWriter) (app.Worker, error) {
			return settings.NewMenuWorker(tx, store, menus, settings.WorkerOptions{Now: time.Now, InitialDelay: cfg.RetryInitial, MaximumDelay: cfg.RetryMaximum})
		}}, {Name: "menu_sharing", New: func(tx runtimecontract.Transactor, _ runtimecontract.GroupMenuWriter) (app.Worker, error) {
			if ledger == nil {
				return nil, errors.New("sharing: delivery ledger not initialized")
			}

			worker, err := sharing.NewProjector(tx, ledger, catalog, store.Resolve, time.Now)
			if err != nil {
				return nil, err
			}

			projector = worker
			return worker, nil
		}}},
	}, nil
}
