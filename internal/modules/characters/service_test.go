package characters

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
)

type memory struct {
	data map[[2]int64]Character
}

func (m *memory) FindCharacter(_ context.Context, g core.GroupID, u core.UserID) (Character, bool, error) {
	c, ok := m.data[[2]int64{
		int64(g),
		int64(u),
	}]
	return c, ok, nil
}

func (m *memory) EnsureCharacter(_ context.Context, c Character) (Character, error) {
	k := [2]int64{
		int64(c.Group),
		int64(c.User),
	}
	if saved, ok := m.data[k]; ok {
		return saved, nil
	}

	m.data[k] = c
	return c, nil
}

func (m *memory) RenameCharacter(_ context.Context, g core.GroupID, u core.UserID, name string) (Character, error) {
	k := [2]int64{
		int64(g),
		int64(u),
	}
	c := m.data[k]
	c.Name = name
	m.data[k] = c
	return c, nil
}

func (m *memory) LoadCharacters(context.Context) ([]Character, error) {
	return nil, nil
}

func TestNamesAndScope(t *testing.T) {
	repo := &memory{data: map[[2]int64]Character{}}
	n := 0
	service, err := New(repo, func() (core.ID, error) {
		n++
		return core.ID(fmt.Sprint(n)), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	scope := core.Scope{
		Group: -1,
		Actor: 1,
	}
	user := core.User{
		ID:        1,
		Username:  "alice",
		FirstName: "Alice",
		LastName:  "Smith",
	}
	a, err := service.Profile(ctx, scope, user)
	if err != nil || a.Name != "alice" {
		t.Fatalf("initial: %+v %v", a, err)
	}

	user.Username = "changed"
	again, err := service.Profile(ctx, scope, user)
	if err != nil || again != a {
		t.Fatal("saved name changed")
	}

	renamed, err := service.Rename(ctx, scope, user, "  🎲`[Имя]  ")
	if err != nil || renamed.Name != "🎲`[Имя]" {
		t.Fatalf("rename: %+v %v", renamed, err)
	}

	for _, name := range []string{
		"",
		"  \n ",
		strings.Repeat("я", 65),
	} {
		if _, err = service.Rename(ctx, scope, user, name); err == nil {
			t.Fatal("invalid name accepted")
		}
	}

	if _, err = ValidateName(strings.Repeat("я", 64)); err != nil {
		t.Fatal(err)
	}

	if _, err = service.Rename(ctx, core.Scope{
		Group: -1,
		Actor: 2,
	}, user, "stolen"); err == nil {
		t.Fatal("foreign rename accepted")
	}

	user.Username = ""
	other, err := service.Profile(ctx, core.Scope{
		Group: -2,
		Actor: 1,
	}, user)
	if err != nil || other.Name != "Alice Smith" || other.ID == a.ID {
		t.Fatalf("group identity: %+v %v", other, err)
	}
}
