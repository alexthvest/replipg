package main

import (
	"errors"
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/devsmoke"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const envDevCharacter = "REPLIPG_DEV_CHARACTER"

func productionOptions(cfg config.Config, lookup config.LookupEnv) (app.Options, error) {
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
	options, err := characterModeOptions(tuning, time.Now, development)
	if err != nil {
		return app.Options{}, err
	}
	register := options.Register
	options.Register = func(registrations app.Registrations) error {
		if err := register(registrations); err != nil {
			return err
		}
		if cfg.DevSmoke {
			return devsmoke.Register(registrations.Registry, nil)
		}
		return nil
	}
	return options, nil
}
func characterOptions(tuning character.Config, clock func() time.Time) (app.Options, error) {
	return characterModeOptions(tuning, clock, false)
}

func characterModeOptions(tuning character.Config, clock func() time.Time, development bool) (app.Options, error) {
	service, err := character.NewService(tuning, clock)
	if err != nil {
		return app.Options{}, err
	}
	commands := []runtimecontract.CommandDeclaration{{Name: "character", Description: "Inspect your character", Ephemeral: true}}
	if development {
		commands = append(commands, runtimecontract.CommandDeclaration{Name: "dev_character_damage", Description: "Damage your test character", Ephemeral: true}, runtimecontract.CommandDeclaration{Name: "dev_character_defeat", Description: "Defeat your test character", Ephemeral: true})
	}
	return app.Options{Commands: commands, Register: func(registrations app.Registrations) error {
		if err := character.Register(registrations.Registry, service, time.Now); err != nil {
			return err
		}
		if development {
			return character.RegisterDevelopment(registrations.Registry, service, time.Now)
		}
		return nil
	}}, nil
}
