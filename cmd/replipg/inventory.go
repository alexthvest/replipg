package main

import (
	"errors"
	"strconv"

	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const envDevInventory = "REPLIPG_DEV_INVENTORY"

const envOwnerTelegramUserID = "REPLIPG_OWNER_TELEGRAM_USER_ID"

func inventoryDevelopmentOwner(lookup config.LookupEnv) (runtimecontract.UserID, error) {
	invalid := errors.New("REPLIPG_OWNER_TELEGRAM_USER_ID: positive decimal int64 required when inventory development is enabled")
	if lookup == nil {
		return 0, invalid
	}

	value, present := lookup(envOwnerTelegramUserID)
	if !present || value == "" {
		return 0, invalid
	}

	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, invalid
		}
	}

	owner, err := strconv.ParseInt(value, 10, 64)
	if err != nil || owner <= 0 {
		return 0, invalid
	}

	return runtimecontract.UserID(owner), nil
}

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
