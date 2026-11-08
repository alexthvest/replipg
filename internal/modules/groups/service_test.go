package groups

import (
	"context"
	"errors"
	"testing"

	"github.com/alexthvest/replipg/internal/core"
)

type memory struct {
	states map[core.GroupID]State
}

func (m *memory) Group(_ context.Context, id core.GroupID) (State, error) {
	s, ok := m.states[id]
	if !ok {
		s = State{
			ID:       id,
			Language: "en",
		}
	}
	return s, nil
}

func (m *memory) LoadGroups(context.Context) ([]State, error) {
	return nil, nil
}

func (m *memory) SetAdmin(ctx context.Context, id core.GroupID, user core.UserID, add bool) (State, error) {
	s, _ := m.Group(ctx, id)
	s.Admins = nil
	if add {
		s.Admins = []core.UserID{user}
	}

	m.states[id] = s
	return s, nil
}

func (m *memory) SetLanguage(ctx context.Context, id core.GroupID, language string) (State, error) {
	s, _ := m.Group(ctx, id)
	s.Language = language
	m.states[id] = s
	return s, nil
}

func TestPermissions(t *testing.T) {
	ctx := context.Background()
	repo := &memory{states: map[core.GroupID]State{}}
	s, err := New(repo, 1)
	if err != nil {
		t.Fatal(err)
	}

	owner := core.Scope{
		Group: -1,
		Actor: 1,
	}
	admin := core.Scope{
		Group: -1,
		Actor: 2,
	}
	if _, err = s.Admin(ctx, admin, 3, true); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}

	if _, err = s.Admin(ctx, owner, 0, true); err == nil {
		t.Fatal("bad recipient")
	}

	if _, err = s.Admin(ctx, owner, 2, true); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		s    core.Scope
		want bool
	}{
		{
			owner,
			true,
		},
		{
			admin,
			true,
		},
		{
			core.Scope{
				Group: -2,
				Actor: 2,
			},
			false,
		},
		{
			core.Scope{
				Group: -1,
				Actor: 3,
			},
			false,
		},
	} {
		got, err := s.Allowed(ctx, tc.s)
		if err != nil || got != tc.want {
			t.Fatalf("permission: %+v %v %v", tc.s, got, err)
		}
	}

	if _, err = s.Admin(ctx, owner, 2, false); err != nil {
		t.Fatal(err)
	}

	if ok, _ := s.Allowed(ctx, admin); ok {
		t.Fatal("revoked permission retained")
	}

	if _, err = New(nil, 1); err == nil {
		t.Fatal("nil repository")
	}
}
