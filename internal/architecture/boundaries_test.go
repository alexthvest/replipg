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
		"internal/runtimecontract/database.go": {"context", "database/sql", "time"},
		"internal/telegram/delivery.go":        {"github.com/go-telegram/bot", "github.com/go-telegram/bot/models", modulePath + "/internal/runtimecontract"},
		"internal/persistence/db.go":           {"modernc.org/sqlite", "modernc.org/libc", modulePath + "/internal/config"},
		"internal/devsmoke/register.go":        {modulePath + "/internal/interaction", modulePath + "/internal/runtimecontract"},
		"internal/character/commands.go":       {modulePath + "/internal/runtimecontract", modulePath + "/internal/interaction", modulePath + "/internal/outbox", modulePath + "/internal/scheduler", modulePath + "/internal/character/model"},
		"cmd/replipg/main.go":                  {modulePath + "/internal/app", modulePath + "/internal/config", modulePath + "/internal/devsmoke"},
		// Cross-component integration tests may compose concrete adapters.
		"internal/character/integration_test.go": {modulePath + "/internal/telegram", "modernc.org/sqlite"},
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
	// Features consume contracts and registries, or their own subpackages.
	return target == owner || target == "runtimecontract" || target == "interaction" || target == "outbox" || target == "scheduler"
}
