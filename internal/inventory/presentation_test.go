package inventory

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/interaction"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
)

func englishPresentation(t *testing.T) Presentation {
	t.Helper()
	c, err := localization.Load()
	if err != nil {
		t.Fatal(err)
	}
	return Presentation{Catalog: c, Resolve: func(context.Context, runtimecontract.DBTX, runtimecontract.ChatID) (localization.Locale, error) {
		return localization.English, nil
	}}
}

// Custom display text belongs to translation fixtures, never gameplay definitions.
func presentationWithItemText(t *testing.T, items map[string]localization.ItemText) Presentation {
	t.Helper()
	resources := map[string][]byte{}
	for _, locale := range []string{"en", "ru"} {
		data, err := os.ReadFile("../localization/locales/" + locale + ".json")
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]string{}
		if err := json.Unmarshal(data, &values); err != nil {
			t.Fatal(err)
		}
		for id, item := range items {
			values["items."+id+".name"] = item.Name
			values["items."+id+".description"] = item.Description
		}
		resources[locale], err = json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := localization.Parse(resources["en"], resources["ru"])
	if err != nil {
		t.Fatal(err)
	}
	p := englishPresentation(t)
	p.Catalog = c
	return p
}

func TestRegistrationRequiresPresentation(t *testing.T) {
	_, catalog, clock, _ := inventoryFixture(t)
	service := inventoryService(t, catalog, inventoryCharacters(t, clock.Now), clock.Now)
	valid := englishPresentation(t)
	for _, register := range []func(*interaction.Registry, Presentation) error{
		func(registry *interaction.Registry, p Presentation) error {
			return Register(registry, service, time.Now, p)
		},
		func(registry *interaction.Registry, p Presentation) error {
			return RegisterDevelopment(registry, service, time.Now, p)
		},
	} {
		for _, p := range []Presentation{{}, {Catalog: valid.Catalog}, {Resolve: valid.Resolve}} {
			registry := interaction.NewRegistry()
			if err := register(registry, p); err == nil {
				t.Fatal("missing presentation dependency accepted")
			}
			if err := register(registry, valid); err != nil {
				t.Fatalf("failed validation reserved routes: %v", err)
			}
		}
	}
}
