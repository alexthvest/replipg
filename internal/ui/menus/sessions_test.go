package menus

import (
	"fmt"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/core"
)

func TestBindingsExpiryAndStaleActions(t *testing.T) {
	now := time.Unix(0, 0)
	n := 0
	clock := func() time.Time {
		return now
	}
	token := func() (string, error) {
		n++
		return fmt.Sprint(n), nil
	}
	pool, err := New(time.Minute, 2, clock, token)
	if err != nil {
		t.Fatal(err)
	}

	scope := core.Scope{
		Group: -1,
		Actor: 2,
	}
	session, err := pool.Create("inventory", scope, scope.Actor)
	if err != nil {
		t.Fatal(err)
	}

	refs, err := pool.Actions(session, []Action{
		{
			Kind:  "inspect",
			Entry: "entry-A",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err = pool.Resolve(refs[0], scope, 100); err == nil {
		t.Fatal("unbound menu accepted")
	}

	if _, err = pool.Bind(session.Token, scope, 100); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		scope   core.Scope
		message int
		data    string
	}{
		{
			core.Scope{
				Group: -2,
				Actor: 2,
			},
			100,
			refs[0],
		},
		{
			core.Scope{
				Group: -1,
				Actor: 3,
			},
			100,
			refs[0],
		},
		{
			scope,
			999,
			refs[0],
		},
		{
			scope,
			100,
			refs[0] + "forged",
		},
	} {
		if _, _, err = pool.Resolve(tc.data, tc.scope, tc.message); err == nil {
			t.Fatal("foreign binding accepted")
		}
	}

	if _, action, err := pool.Resolve(refs[0], scope, 100); err != nil || action.Entry != "entry-A" {
		t.Fatalf("resolve: %+v %v", action, err)
	}

	if _, err = pool.Actions(session, []Action{
		{
			Kind:  "inspect",
			Entry: "entry-B",
		},
	}); err != nil {
		t.Fatal(err)
	}

	if _, _, err = pool.Resolve(refs[0], scope, 100); err == nil {
		t.Fatal("stale page mapped to new entry")
	}

	now = now.Add(time.Minute)
	pool.Sweep()
	if len(pool.pool) != 0 {
		t.Fatal("expired session retained")
	}

	if _, _, err = pool.Resolve(refs[0], scope, 100); err == nil {
		t.Fatal("expired menu accepted")
	}

	restarted, _ := New(time.Minute, 2, clock, token)
	if _, _, err = restarted.Resolve(refs[0], scope, 100); err == nil {
		t.Fatal("restart retained menu")
	}
}

func TestEvictionAndCollisions(t *testing.T) {
	now := time.Unix(0, 0)
	n := 0
	scope := core.Scope{
		Group: -1,
		Actor: 1,
	}
	pool, _ := New(time.Minute, 1, func() time.Time {
		return now
	}, func() (string, error) {
		n++
		return fmt.Sprint(n), nil
	})
	first, err := pool.Create("settings", scope, 0)
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Second)
	if _, err = pool.Create("settings", scope, 0); err != nil {
		t.Fatal(err)
	}

	if pool.pool[first.Token] != nil || len(pool.pool) != 1 {
		t.Fatal("pool limit not enforced")
	}

	colliding, _ := New(time.Minute, 2, func() time.Time {
		return now
	}, func() (string, error) {
		return "constant", nil
	})
	if _, err = colliding.Create("settings", scope, 0); err != nil {
		t.Fatal(err)
	}

	if _, err = colliding.Create("settings", scope, 0); err == nil {
		t.Fatal("token collision accepted")
	}

	random, err := RandomToken()
	if err != nil || len(random) > 32 {
		t.Fatalf("token: %v", err)
	}
}
