package architecture_test

import (
	"fmt"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

const modulePath = "github.com/alexthvest/replipg"

func TestProductionBoundaries(t *testing.T) {
	root := filepath.Join("..", "..")
	violations, err := checkBoundaries(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}

func TestBoundaryCheckerRejectsForbiddenImports(t *testing.T) {
	cases := []struct {
		name, source, target string
	}{
		{"runtime imports localization", "internal/app/runtime.go", modulePath + "/internal/localization"},
		{"runtime imports settings", "internal/interaction/dispatch.go", modulePath + "/internal/settings"},
		{"catalog imports settings", "internal/localization/catalog.go", modulePath + "/internal/settings"},
		{"catalog imports external", "internal/localization/catalog.go", "example.com/catalog"},
		{"settings imports persistence", "internal/settings/store.go", modulePath + "/internal/persistence"},
		{"settings imports gameplay", "internal/settings/store.go", modulePath + "/internal/character"},
		{"settings imports transport", "internal/settings/commands.go", modulePath + "/internal/telegram"},
		{"settings imports localization subpackage", "internal/settings/commands.go", modulePath + "/internal/localization/extra"},
		{"character imports settings", "internal/character/commands.go", modulePath + "/internal/settings"},
		{"inventory imports settings", "internal/inventory/commands.go", modulePath + "/internal/settings"},
		{"Telegram model leakage", "internal/interaction/events.go", "github.com/go-telegram/bot/models"},
		{"SQLite driver leakage", "internal/scheduler/store.go", "modernc.org/sqlite"},
		{"SQLite support leakage", "internal/outbox/store.go", "modernc.org/libc"},
		{"contract imports adapter", "internal/runtimecontract/work.go", modulePath + "/internal/telegram"},
		{"contract imports third party", "internal/runtimecontract/work.go", "example.com/types"},
		{"contract imports module subpackage", "internal/runtimecontract/work.go", modulePath + "/internal/runtimecontract/adapter"},
		{"platform-specific leakage", "internal/interaction/events_windows.go", "github.com/go-telegram/bot/models"},
		{"runtime imports gameplay", "internal/interaction/dispatch.go", modulePath + "/internal/character"},
		{"runtime imports nested gameplay", "internal/scheduler/worker.go", modulePath + "/internal/game/battle"},
		{"runtime imports development feature", "internal/app/runtime.go", modulePath + "/internal/devsmoke"},
		{"feature imports transport", "internal/character/commands.go", modulePath + "/internal/telegram"},
		{"feature imports concrete database", "internal/character/store.go", modulePath + "/internal/persistence"},
		{"persistence imports service", "internal/persistence/db.go", modulePath + "/internal/outbox"},
		{"service imports app", "internal/outbox/store.go", modulePath + "/internal/app"},
		{"runtime imports command", "internal/health/state.go", modulePath + "/cmd/replipg"},
		{"runtime imports character contract", "internal/app/runtime.go", modulePath + "/internal/character/contract"},
		{"gameplay imports character implementation", "internal/battle/service.go", modulePath + "/internal/character"},
		{"nested gameplay imports character implementation", "internal/inventory/nested/store.go", modulePath + "/internal/character/store"},
		{"gameplay imports nested character contract", "internal/encounter/events_windows.go", modulePath + "/internal/character/contract/extra"},
		{"character contract imports implementation", "internal/character/contract/types.go", modulePath + "/internal/character"},
		{"nested character contract imports interaction", "internal/character/contract/nested/types_linux.go", modulePath + "/internal/interaction"},
		{"character contract imports runtime subpackage", "internal/character/contract/types.go", modulePath + "/internal/runtimecontract/extra"},
		{"character contract imports third party", "internal/character/contract/types.go", "example.com/types"},
		{"character imports outbox", "internal/character/commands.go", modulePath + "/internal/outbox"},
		{"character imports scheduler", "internal/character/nested/rules_windows.go", modulePath + "/internal/scheduler"},
		{"character imports config", "internal/character/config.go", modulePath + "/internal/config"},
		{"character imports diagnostics", "internal/character/service.go", modulePath + "/internal/observability"},
		{"character imports other gameplay", "internal/character/service.go", modulePath + "/internal/battle"},
		{"character imports contract subpackage", "internal/character/service.go", modulePath + "/internal/character/contract/extra"},
		{"runtime imports inventory", "internal/app/runtime.go", modulePath + "/internal/inventory"},
		{"runtime imports inventory contract", "internal/scheduler/worker_windows.go", modulePath + "/internal/inventory/contract"},
		{"nested runtime imports inventory implementation", "internal/app/nested/runtime_windows.go", modulePath + "/internal/inventory/store"},
		{"persistence imports inventory", "internal/persistence/db.go", modulePath + "/internal/inventory"},
		{"character imports inventory contract", "internal/character/service.go", modulePath + "/internal/inventory/contract"},
		{"character contract imports inventory contract", "internal/character/contract/types.go", modulePath + "/internal/inventory/contract"},
		{"gameplay imports inventory implementation", "internal/battle/service.go", modulePath + "/internal/inventory"},
		{"nested gameplay imports inventory implementation", "internal/battle/nested/service_windows.go", modulePath + "/internal/inventory/store"},
		{"gameplay imports nested inventory contract", "internal/encounter/events.go", modulePath + "/internal/inventory/contract/extra"},
		{"inventory imports outbox", "internal/inventory/service.go", modulePath + "/internal/outbox"},
		{"inventory imports scheduler", "internal/inventory/nested/service_windows.go", modulePath + "/internal/scheduler"},
		{"inventory imports transport", "internal/inventory/commands.go", modulePath + "/internal/telegram"},
		{"inventory imports concrete storage", "internal/inventory/store.go", modulePath + "/internal/persistence"},
		{"inventory imports driver", "internal/inventory/store.go", "modernc.org/sqlite"},
		{"inventory imports transport model", "internal/inventory/commands.go", "github.com/go-telegram/bot/models"},
		{"inventory imports config", "internal/inventory/config.go", modulePath + "/internal/config"},
		{"inventory imports diagnostics", "internal/inventory/service.go", modulePath + "/internal/observability"},
		{"inventory imports other gameplay", "internal/inventory/service.go", modulePath + "/internal/battle"},
		{"inventory imports runtime subpackage", "internal/inventory/service.go", modulePath + "/internal/runtimecontract/extra"},
		{"inventory imports interaction subpackage", "internal/inventory/service.go", modulePath + "/internal/interaction/extra"},
		{"inventory imports contract subpackage", "internal/inventory/service.go", modulePath + "/internal/inventory/contract/extra"},
		{"inventory imports third party", "internal/inventory/service.go", "example.com/types"},
		{"inventory contract imports implementation", "internal/inventory/contract/types.go", modulePath + "/internal/inventory"},
		{"inventory contract imports own subpackage", "internal/inventory/contract/types.go", modulePath + "/internal/inventory/contract/extra"},
		{"nested inventory contract imports interaction", "internal/inventory/contract/nested/types_linux.go", modulePath + "/internal/interaction"},
		{"inventory contract imports character service", "internal/inventory/contract/types.go", modulePath + "/internal/character"},
		{"inventory contract imports transport", "internal/inventory/contract/types.go", modulePath + "/internal/telegram"},
		{"inventory contract imports concrete storage", "internal/inventory/contract/types.go", modulePath + "/internal/persistence"},
		{"inventory contract imports driver", "internal/inventory/contract/types.go", "modernc.org/sqlite"},
		{"inventory contract imports outbox", "internal/inventory/contract/types.go", modulePath + "/internal/outbox"},
		{"inventory contract imports scheduler", "internal/inventory/contract/types.go", modulePath + "/internal/scheduler"},
		{"inventory contract imports nested character contract", "internal/inventory/contract/types.go", modulePath + "/internal/character/contract/extra"},
		{"inventory contract imports runtime subpackage", "internal/inventory/contract/types.go", modulePath + "/internal/runtimecontract/extra"},
		{"inventory contract imports third party", "internal/inventory/contract/types.go", "example.com/types"},
		{"unapproved runtime dependency", "internal/telegram/client.go", "example.com/client"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := fstest.MapFS{tc.source: {Data: []byte("package fixture\nimport _ " + `"` + tc.target + `"` + "\n")}}
			violations, err := checkBoundaries(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if len(violations) != 1 || !strings.Contains(violations[0], tc.source) || !strings.Contains(violations[0], tc.target) {
				t.Fatalf("expected source and forbidden import in one diagnostic, got %v", violations)
			}
		})
	}
}

func TestBoundaryCheckerAllowsContractsAndComposition(t *testing.T) {
	fixture := fstest.MapFS{}
	for source, imports := range map[string][]string{
		"internal/settings/commands.go":                     {"context", modulePath + "/internal/localization", modulePath + "/internal/interaction", modulePath + "/internal/runtimecontract"},
		"internal/localization/catalog.go":                  {"embed", "encoding/json", "text/template"},
		"internal/character/render.go":                      {modulePath + "/internal/localization"},
		"internal/inventory/render.go":                      {modulePath + "/internal/localization"},
		"internal/devsmoke/render.go":                       {modulePath + "/internal/localization"},
		"internal/runtimecontract/database.go":              {"context", "database/sql", "time"},
		"internal/telegram/delivery.go":                     {"github.com/go-telegram/bot", "github.com/go-telegram/bot/models", modulePath + "/internal/runtimecontract"},
		"internal/persistence/db.go":                        {"modernc.org/sqlite", "modernc.org/libc", modulePath + "/internal/config"},
		"internal/devsmoke/register.go":                     {modulePath + "/internal/interaction", modulePath + "/internal/runtimecontract"},
		"internal/character/commands.go":                    {modulePath + "/internal/runtimecontract", modulePath + "/internal/interaction", modulePath + "/internal/character/contract", modulePath + "/internal/character/model"},
		"internal/character/contract/types.go":              {"errors", "time", modulePath + "/internal/runtimecontract"},
		"internal/battle/future/events_windows.go":          {modulePath + "/internal/character/contract", modulePath + "/internal/runtimecontract"},
		"internal/inventory/future.go":                      {modulePath + "/internal/character/contract"},
		"internal/inventory/nested/service_windows.go":      {"context", modulePath + "/internal/inventory", modulePath + "/internal/inventory/catalog", modulePath + "/internal/inventory/contract", modulePath + "/internal/runtimecontract", modulePath + "/internal/interaction", modulePath + "/internal/character/contract"},
		"internal/inventory/contract/types.go":              {"errors", "time", modulePath + "/internal/runtimecontract", modulePath + "/internal/character/contract"},
		"internal/inventory/contract/nested/types_linux.go": {"context", modulePath + "/internal/runtimecontract", modulePath + "/internal/character/contract"},
		"internal/battle/nested/service_windows.go":         {modulePath + "/internal/inventory/contract", modulePath + "/internal/character/contract", modulePath + "/internal/runtimecontract"},
		"cmd/replipg/main.go":                               {modulePath + "/internal/app", modulePath + "/internal/config", modulePath + "/internal/devsmoke", modulePath + "/internal/character", modulePath + "/internal/character/contract", modulePath + "/internal/inventory", modulePath + "/internal/inventory/contract"},
		// Cross-component integration tests may compose concrete adapters.
		"internal/character/integration_test.go": {modulePath + "/internal/telegram", "modernc.org/sqlite"},
		"internal/inventory/integration_test.go": {modulePath + "/internal/persistence", modulePath + "/internal/character", "modernc.org/sqlite"},
	} {
		var contents strings.Builder
		contents.WriteString("package fixture\n")
		for _, dependency := range imports {
			contents.WriteString("import _ " + `"` + dependency + `"` + "\n")
		}
		fixture[source] = &fstest.MapFile{Data: []byte(contents.String())}
	}
	violations, err := checkBoundaries(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("approved dependency edges rejected: %v", violations)
	}
}

// Check every production Go source, including files hidden by platform/build tags.
// Integration tests intentionally compose adapters and are outside this gate.
func checkBoundaries(root fs.FS) ([]string, error) {
	var violations []string
	stdlib := make(map[string]bool)
	err := fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != "." && path != "cmd" && path != "internal" && !strings.HasPrefix(path, "cmd/") && !strings.HasPrefix(path, "internal/") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			dependency, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			standard, checked := stdlib[dependency]
			if !checked {
				// A dotted first element is a module path, never a standard
				// package. Do not ask the build tool to resolve external modules.
				if !strings.Contains(strings.Split(dependency, "/")[0], ".") {
					pkg, err := build.Default.Import(dependency, "", build.FindOnly)
					standard = err == nil && pkg.Goroot
				}
				stdlib[dependency] = standard
			}
			if !standard && !allowedDependency(path, dependency) {
				violations = append(violations, fmt.Sprintf("%s: forbidden dependency %s", path, dependency))
			}
		}
		return nil
	})
	return violations, err
}

func allowedDependency(source, dependency string) bool {
	owner := strings.Split(source, "/")[1]
	if strings.HasPrefix(source, "cmd/") {
		owner = "cmd"
	}
	if strings.HasPrefix(source, "internal/character/contract/") {
		return dependency == modulePath+"/internal/runtimecontract"
	}
	if strings.HasPrefix(source, "internal/inventory/contract/") {
		return dependency == modulePath+"/internal/runtimecontract" ||
			dependency == modulePath+"/internal/character/contract"
	}
	if owner == "localization" {
		return false
	}
	if owner == "settings" {
		return dependency == modulePath+"/internal/localization" || dependency == modulePath+"/internal/interaction" || dependency == modulePath+"/internal/runtimecontract" || dependency == modulePath+"/internal/settings" || strings.HasPrefix(dependency, modulePath+"/internal/settings/")
	}
	if owner == "inventory" {
		return dependency == modulePath+"/internal/localization" || dependency == modulePath+"/internal/runtimecontract" ||
			dependency == modulePath+"/internal/interaction" ||
			dependency == modulePath+"/internal/character/contract" ||
			dependency == modulePath+"/internal/inventory" ||
			strings.HasPrefix(dependency, modulePath+"/internal/inventory/") && !strings.HasPrefix(dependency, modulePath+"/internal/inventory/contract/")
	}
	if owner == "character" {
		return dependency == modulePath+"/internal/localization" || dependency == modulePath+"/internal/runtimecontract" ||
			dependency == modulePath+"/internal/interaction" ||
			dependency == modulePath+"/internal/character" ||
			strings.HasPrefix(dependency, modulePath+"/internal/character/") && !strings.HasPrefix(dependency, modulePath+"/internal/character/contract/")
	}
	if owner == "runtimecontract" {
		return false // All standard-library imports were accepted by the scanner.
	}
	// Strictly decreasing layers follow the design's Dependency direction.
	// Subpackages retain their component's ownership; Go checks their cycles.
	layers := map[string]int{
		"runtimecontract": 0, "config": 0, "observability": 1,
		"persistence": 2,
		"interaction": 3, "outbox": 3, "scheduler": 3,
		"telegram": 4, "health": 4,
		"app": 5, "cmd": 6,
	}
	if dependency == "github.com/go-telegram/bot" || strings.HasPrefix(dependency, "github.com/go-telegram/bot/") {
		return owner == "telegram"
	}
	if dependency == "modernc.org/sqlite" || strings.HasPrefix(dependency, "modernc.org/sqlite/") || dependency == "modernc.org/libc" || strings.HasPrefix(dependency, "modernc.org/libc/") {
		return owner == "persistence"
	}
	if !strings.HasPrefix(dependency, modulePath+"/internal/") {
		return false
	}
	target := strings.Split(strings.TrimPrefix(dependency, modulePath+"/internal/"), "/")[0]
	ownerLayer, runtimeOwner := layers[owner]
	targetLayer, runtimeTarget := layers[target]
	if runtimeOwner {
		// The command composes optional features; runtime never imports them.
		return owner == "cmd" || runtimeTarget && (owner == target || targetLayer < ownerLayer)
	}
	// Gameplay may consume only the exact shared feature contract packages.
	if dependency == modulePath+"/internal/character/contract" || dependency == modulePath+"/internal/inventory/contract" {
		return true
	}
	// Other features retain their existing contracts, registries, and own subpackages.
	return target == owner || dependency == modulePath+"/internal/localization" || target == "runtimecontract" || target == "interaction" || target == "outbox" || target == "scheduler"
}
