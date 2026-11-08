package cueassets

import (
	"fmt"
	"path/filepath"
	"sort"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cueerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/load"
)

// evaluate builds only the captured package bytes, in a fresh evaluation context.
func evaluate(dir string, files map[string][]byte, dst any) error {
	overlay := make(map[string]load.Source, len(files))
	var names []string

	for path, data := range files {
		overlay[path] = load.FromBytes(data)
		if filepath.Dir(path) == dir {
			names = append(names, filepath.Base(path))
		}
	}

	sort.Strings(names)
	instances := load.Instances(names, &load.Config{
		Dir:     dir,
		Overlay: overlay,
	})
	if len(instances) != 1 {
		return fmt.Errorf("load %s: expected one package", dir)
	}

	value := cuecontext.New().BuildInstance(instances[0])
	if err := value.Validate(cue.Concrete(true)); err != nil {
		return fmt.Errorf("validate %s: %w\n%s", dir, err, cueerrors.Details(err, nil))
	}

	if err := value.Decode(dst); err != nil {
		return fmt.Errorf("decode %s: %w", dir, err)
	}
	return nil
}
