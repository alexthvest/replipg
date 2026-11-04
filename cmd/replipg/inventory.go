package main

import (
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/inventory"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const envDevInventory = "REPLIPG_DEV_INVENTORY"

func inventoryDevelopmentMode(lookup config.LookupEnv) (bool, error) {
	if lookup != nil {
		if value, present := lookup(envDevInventory); present {
			switch value {
			case "true":
				return true, nil
			case "false":
				return false, nil
			default:
				return false, errors.New("REPLIPG_DEV_INVENTORY: must be true or false")
			}
		}
	}
	return false, nil
}

func inventoryOptions(options app.Options, characters *character.Service, clock func() time.Time, development bool) (app.Options, error) {
	catalog, err := inventory.LoadCatalog()
	if err != nil {
		return app.Options{}, err
	}
	service, err := inventory.NewService(catalog, characters, clock)
	if err != nil {
		return app.Options{}, err
	}
	options.Prepare = service.Prepare
	options.Commands = append(options.Commands, runtimecontract.CommandDeclaration{Name: "inventory", Description: "Inspect and equip your items", Ephemeral: true})
	if development {
		options.Commands = append(options.Commands, runtimecontract.CommandDeclaration{Name: "dev_inventory_grant", Description: "Grant your test character items", Ephemeral: true}, runtimecontract.CommandDeclaration{Name: "dev_inventory_heal", Description: "Heal your test character with an item", Ephemeral: true})
	}
	register := options.Register
	options.Register = func(registrations app.Registrations) error {
		if register != nil {
			if err := register(registrations); err != nil {
				return err
			}
		}
		if err := inventory.Register(registrations.Registry, service, time.Now); err != nil {
			return err
		}
		if development {
			return inventory.RegisterDevelopment(registrations.Registry, service, time.Now)
		}
		return nil
	}
	return options, nil
}
