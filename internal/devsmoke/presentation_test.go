package devsmoke

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
	valid := englishPresentation(t)
	for _, p := range []Presentation{{}, {Catalog: valid.Catalog}, {Resolve: valid.Resolve}} {
		registry := interaction.NewRegistry()
		if err := Register(registry, time.Now, p); err == nil {
			t.Fatal("missing presentation dependency accepted")
		}
		if err := Register(registry, time.Now, valid); err != nil {
			t.Fatalf("failed validation reserved routes: %v", err)
		}
	}
}
