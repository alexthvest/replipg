// Package cueassets captures and evaluates coherent CUE asset candidates.
package cueassets

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alexthvest/replipg/internal/content"
	"github.com/alexthvest/replipg/internal/modules/items"
)

func capture(root string) (map[string][]byte, error) {
	files := map[string][]byte{}

	for _, name := range []string{"items", "locales"} {
		dir := filepath.Join(root, name)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("read assets %s: %w", dir, err)
		}

		count := 0

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".cue") {
				continue
			}

			path := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("capture %s: %w", path, err)
			}

			files[path] = data
			count++
		}

		if count == 0 {
			return nil, fmt.Errorf("assets %s: no CUE files", dir)
		}
	}
	return files, nil
}

func same(a, b map[string][]byte) bool {
	if len(a) != len(b) {
		return false
	}

	for path, data := range a {
		other, ok := b[path]
		if !ok || !bytes.Equal(data, other) {
			return false
		}
	}
	return true
}

func stableCapture(root string, read func(string) (map[string][]byte, error)) (map[string][]byte, error) {
	var last error

	for range 3 {
		first, err := read(root)
		if err != nil {
			last = err
			continue
		}

		second, err := read(root)
		if err != nil {
			last = err
			continue
		}

		if same(first, second) {
			return first, nil
		}

		last = fmt.Errorf("assets changed during capture")
	}
	return nil, fmt.Errorf("capture assets after retries: %w", last)
}

func Load(root string) (*content.Bundle, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	captured, err := stableCapture(root, capture)
	if err != nil {
		return nil, err
	}

	definitions := map[string]items.Definition{}
	translations := map[string]map[string]string{}
	if err = evaluate(filepath.Join(root, "items"), captured, &definitions); err != nil {
		return nil, err
	}

	if err = evaluate(filepath.Join(root, "locales"), captured, &translations); err != nil {
		return nil, err
	}
	return content.New(definitions, translations)
}
