package character

import (
	"context"
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

func TestRegistrationRequiresPresentation(t *testing.T) {
	service := &fakeHealthService{}
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
