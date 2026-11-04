package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/alexthvest/replipg/internal/config"
	"github.com/alexthvest/replipg/internal/localization"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalizationProductionComposition(t *testing.T) {
	options, err := productionOptions(config.Config{DevSmoke: true}, commandLookup(map[string]string{envDevCharacter: "true", envDevInventory: "true"}))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"character", "dev_character_damage", "dev_character_defeat", "inventory", "dev_inventory_grant", "dev_inventory_heal", "settings", "dev_smoke", "dev_smoke_ephemeral"}
	if len(options.Commands) != len(want) {
		t.Fatalf("complete enabled menu: got %d want %d", len(options.Commands), len(want))
	}
	for i, name := range want {
		if options.Commands[i].Name != name || options.Commands[i].Description == "" {
			t.Fatalf("command %d missing translated declaration", i)
		}
	}
}

type unavailableMembership struct{}

func (unavailableMembership) VerifyGroupMember(context.Context, runtimecontract.ChatID, runtimecontract.UserID) (runtimecontract.MembershipResult, error) {
	return runtimecontract.MembershipResult{}, nil
}

// Pagination builds a test-only extended catalog. Its required translation
// manifest and both embedded resources travel with the overlaid item content.
func localizedPaginationOverlay(t *testing.T, source, replacement string) map[string]string {
	texts := map[string][2]string{}
	for n := 0; n < 6; n++ {
		texts[fmt.Sprintf("extra_%d", n)] = [2]string{fmt.Sprintf("Extra %d", n), "Pagination fixture."}
	}
	return localizedItemOverlay(t, source, replacement, texts)
}
func localizedItemOverlay(t *testing.T, source, replacement string, texts map[string][2]string) map[string]string {
	t.Helper()
	replacements := map[string]string{source: replacement}
	dir := t.TempDir()
	manifestPath, err := filepath.Abs("../../internal/localization/manifest.go")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var additions strings.Builder
	for id := range texts {
		for _, field := range []string{"name", "description"} {
			limit := 80
			if field == "description" {
				limit = 400
			}
			fmt.Fprintf(&additions, "Key(%q): {limit:%d,params:map[string]ParamKind{}},\n", "items."+id+"."+field, limit)
		}
	}
	manifest = []byte(strings.Replace(string(manifest), "var manifest = map[Key]messageSpec{", "var manifest = map[Key]messageSpec{\n"+additions.String(), 1))
	target := filepath.Join(dir, "manifest.go")
	if err := os.WriteFile(target, manifest, 0600); err != nil {
		t.Fatal(err)
	}
	replacements[manifestPath] = target
	for _, locale := range []string{"en", "ru"} {
		source, err := filepath.Abs("../../internal/localization/locales/" + locale + ".json")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]string
		if err := json.Unmarshal(raw, &values); err != nil {
			t.Fatal(err)
		}
		for id, text := range texts {
			values["items."+id+".name"] = text[0]
			values["items."+id+".description"] = text[1]
		}
		raw, err = json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(dir, locale+".json")
		if err := os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
		replacements[source] = target
	}
	return replacements
}

func TestLocalizationStartupDiagnosticKeepsSafeCatalogContext(t *testing.T) {
	_, err := localization.Parse([]byte(`{}`), []byte(`{}`))
	if err == nil {
		t.Fatal("incomplete catalog accepted")
	}
	var out bytes.Buffer
	writeRuntimeError(&out, "runtime construction failed", fmt.Errorf("PRIVATE wrapper: %w", err))
	if !strings.Contains(out.String(), "en missing category.armor") || strings.Contains(out.String(), "PRIVATE") {
		t.Fatalf("unsafe or unactionable catalog diagnostic: %q", out.String())
	}
}
