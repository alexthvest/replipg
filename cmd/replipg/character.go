package main

import (
	"time"

	"github.com/alexthvest/replipg/internal/app"
	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

const envDevCharacter = "REPLIPG_DEV_CHARACTER"

func characterOptions(tuning character.Config, clock func() time.Time) (app.Options, error) {
	return characterModeOptions(tuning, clock, false)
}

func characterModeOptions(tuning character.Config, clock func() time.Time, development bool) (app.Options, error) {
	service, err := character.NewService(tuning, clock)
	if err != nil {
		return app.Options{}, err
	}
	return characterServiceOptions(service, development), nil
}

func characterServiceOptions(service *character.Service, development bool) app.Options {
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
	}}
}
