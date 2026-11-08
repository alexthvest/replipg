package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
	"github.com/alexthvest/replipg/internal/modules/characters"
)

func TestCharacterConcurrentCreationAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "characters.db")
	db, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	service, err := characters.New(db, core.NewID)
	if err != nil {
		t.Fatal(err)
	}

	scope := core.Scope{
		Group: -1,
		Actor: 1,
	}
	user := core.User{
		ID:        1,
		FirstName: "Alice",
	}
	var joined sync.WaitGroup
	results := make(chan characters.Character, 10)
	for range 10 {
		joined.Add(1)
		go func() {
			defer joined.Done()

			c, err := service.Profile(ctx, scope, user)
			if err != nil {
				t.Error(err)
				return
			}

			results <- c
		}()
	}

	joined.Wait()
	close(results)
	var saved characters.Character
	for c := range results {
		if saved.ID == "" {
			saved = c
		}

		if c.ID != saved.ID {
			t.Fatal("duplicate character")
		}
	}

	if _, err = service.Rename(ctx, scope, user, "saved 🎲"); err != nil {
		t.Fatal(err)
	}

	if _, err = service.Profile(ctx, core.Scope{
		Group: -2,
		Actor: 1,
	}, user); err != nil {
		t.Fatal(err)
	}

	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		_ = db.Close()
	}()

	all, err := db.LoadCharacters(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("load: %+v %v", all, err)
	}

	reopened, ok, err := db.FindCharacter(ctx, -1, 1)
	if err != nil || !ok || reopened.ID != saved.ID || reopened.Name != "saved 🎲" {
		t.Fatalf("reopen: %+v %v", reopened, err)
	}
}
