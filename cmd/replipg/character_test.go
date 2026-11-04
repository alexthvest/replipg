package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/character"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func TestProductionCharacterComposition(t *testing.T) {
	options, err := productionOptions(config.Config{}, commandLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	registry := interaction.NewRegistry()
	if options.Register == nil {
		t.Fatal("missing normal behavior")
	}
	if err := options.Register(sharingTestRegistrations(t, registry)); err != nil {
		t.Fatal(err)
	}
	registry.Seal()
	if len(options.Commands) != 3 || options.Commands[0].Name != "character" || options.Commands[1].Name != "inventory" || !options.Commands[0].Ephemeral || !options.Commands[1].Ephemeral || options.Prepare == nil {
		t.Fatal("missing ephemeral menu")
	}
	identity := interaction.Identity{Update: 1, Chat: -100, User: 7, ChatKind: runtimecontract.ChatKindSupergroup}
	for _, declaration := range options.Commands {
		if _, ok := registry.Resolve(interaction.Command{Identity: identity, Name: interaction.CommandName(declaration.Name)}); !ok {
			t.Fatal("menu has no registered handler")
		}
	}
	if _, ok := registry.Resolve(interaction.Command{Identity: identity, Name: "dev_character_damage"}); ok {
		t.Fatal("production exposes health probe")
	}
	if _, err := characterOptions(character.Config{}, time.Now); err == nil {
		t.Fatal("invalid tuning accepted")
	}
}

func TestStrictCharacterDevelopmentMode(t *testing.T) {
	for _, tc := range []struct {
		value                   string
		present, enabled, valid bool
	}{
		{"", false, false, true}, {"true", true, true, true}, {"false", true, false, true}, {"TRUE", true, false, false}, {"1", true, false, false}, {"", true, false, false}, {"true ", true, false, false}, {"private invalid", true, false, false},
	} {
		t.Run(tc.value+fmt.Sprint(tc.present), func(t *testing.T) {
			options, err := productionOptions(config.Config{}, func(key string) (string, bool) {
				if key == envDevCharacter {
					return tc.value, tc.present
				}
				return "", false
			})
			if !tc.valid {
				if err == nil || strings.Contains(err.Error(), "private invalid") {
					t.Fatal("invalid development flag accepted or leaked")
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
			identity := interaction.Identity{Update: 1, Chat: -100, User: 7, ChatKind: runtimecontract.ChatKindSupergroup}
			for _, name := range []interaction.CommandName{"dev_character_damage", "dev_character_defeat"} {
				if _, ok := registry.Resolve(interaction.Command{Identity: identity, Name: name}); ok != tc.enabled {
					t.Fatal("development route mismatch")
				}
			}
			want := 3
			if tc.enabled {
				want = 5
			}
			if len(options.Commands) != want {
				t.Fatal("menu mismatch")
			}
			for _, declaration := range options.Commands {
				if !declaration.Ephemeral {
					t.Fatal("public declaration")
				}
				if _, ok := registry.Resolve(interaction.Command{Identity: identity, Name: interaction.CommandName(declaration.Name)}); !ok {
					t.Fatal("menu missing handler")
				}
			}
		})
	}
}
