package main

import (
	"errors"

	"github.com/alexthvest/replipg/internal/config"
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
