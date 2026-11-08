package app

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
	"testing"
)

func TestFeatureImportBoundaries(t *testing.T) {
	output, err := exec.Command(
		"go",
		"list",
		"-json",
		"../modules/...",
		"../runtime",
		"../ui/menus",
		"../application/...",
	).Output()
	if err != nil {
		t.Fatal(err)
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	count := 0
	for {
		var pkg struct {
			ImportPath string
			Imports    []string
		}
		if err = decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}

		count++
		for _, path := range pkg.Imports {
			forbidden := []string{
				"/internal/adapters/",
				"/internal/app",
				"database/sql",
				"github.com/go-telegram/",
				"cuelang.org/",
			}
			if strings.Contains(pkg.ImportPath, "/internal/modules/") {
				forbidden = append(forbidden, "/internal/application/", "/internal/runtime")
			}

			if strings.HasSuffix(pkg.ImportPath, "/runtime") {
				forbidden = append(forbidden, "/internal/modules/", "/internal/application/", "/internal/ui/")
			}

			if strings.HasSuffix(pkg.ImportPath, "/ui/menus") {
				forbidden = append(forbidden, "/internal/modules/", "/internal/runtime")
			}

			if strings.HasSuffix(pkg.ImportPath, "/characters") || strings.HasSuffix(pkg.ImportPath, "/items") {
				forbidden = append(forbidden, "/internal/modules/inventory")
			}

			if strings.HasSuffix(pkg.ImportPath, "/inventory") {
				forbidden = append(forbidden, "/internal/modules/characters")
			}

			if strings.HasSuffix(pkg.ImportPath, "/groups") {
				forbidden = append(forbidden, "/internal/modules/localization")
			}
			// Application coordinators may depend on their feature contracts, but features
			// and the runtime must never depend on the coordinators.
			if strings.Contains(pkg.ImportPath, "/internal/application/") {
				forbidden = []string{
					"/internal/adapters/",
					"/internal/app/",
					"/internal/runtime",
					"database/sql",
					"github.com/go-telegram/",
					"cuelang.org/",
				}
			}

			for _, boundary := range forbidden {
				if strings.Contains(path, boundary) {
					t.Errorf("%s imports forbidden dependency %s", pkg.ImportPath, path)
				}
			}
		}
	}

	if count == 0 {
		t.Fatal("no packages inspected")
	}
}
