package menus

import (
	"fmt"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/core"
)

func TestStableActionsDependencyIsolationAndExpiry(t *testing.T) {
	now := time.Unix(100, 0)
	i := 0
	pool, err := New(time.Minute, 4, func() time.Time {
		return now
	}, func() (string, error) {
		i++
		return fmt.Sprint(i), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var sessions []*Session
	for i, group := range []core.GroupID{-1, -1, -2} {
		scope := core.Scope{
			Group: group,
			Actor: core.UserID(i + 1),
		}
		session, err := pool.Create("inventory", scope, scope.Actor)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Bind(session.Token, scope, i+1); err != nil {
			t.Fatal(err)
		}
		pool.Depend(session, Dependency{
			Kind:      "inventory",
			Group:     group,
			Character: core.ID(fmt.Sprint(i)),
		})
		pool.Stage(session, core.View{Text: core.Literal("inventory")})
		sessions = append(sessions, session)
	}

	session := sessions[0]
	old, err := pool.Actions(session, []Action{{
		Kind:  "inspect",
		Entry: "A",
	}})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := pool.Actions(session, []Action{{
		Kind:  "inspect",
		Entry: "A",
	}})
	if err != nil || old[0] != refs[0] {
		t.Fatal("identical semantic actions changed references")
	}
	pool.Invalidate(Dependency{
		Kind:      "inventory",
		Group:     -1,
		Character: "0",
	})
	if dirty := pool.Dirty(); len(dirty) != 1 || dirty[0] != session {
		t.Fatalf("invalidation leaked scope: %+v", dirty)
	}

	used := session.Used
	now = now.Add(30 * time.Second)
	pool.Stage(session, core.View{Text: core.Literal("changed")})
	if session.Used != used {
		t.Fatal("background stage extended activity")
	}
	now = now.Add(30 * time.Second)
	if jobs := pool.Claim(4); len(jobs) != 0 || len(pool.pool) != 0 || len(pool.index) != 0 {
		t.Fatal("expired subscriptions retained work or indexes")
	}
}

func TestDeliveryRevisionsBackoffAndPermanentFailure(t *testing.T) {
	now := time.Unix(100, 0)
	pool, _ := New(time.Hour, 3, func() time.Time {
		return now
	}, func() (string, error) {
		return fmt.Sprint(now.UnixNano()), nil
	})
	scope := core.Scope{
		Group: -1,
		Actor: 1,
	}
	session, _ := pool.Create("inventory", scope, 1)
	_, _ = pool.Bind(session.Token, scope, 1)
	pool.Stage(session, core.View{Text: core.Literal("old")})
	old := pool.Claim(3)
	if len(old) != 1 {
		t.Fatal("initial job missing")
	}
	pool.Stage(session, core.View{Text: core.Literal("new")})
	if jobs := pool.Claim(3); len(jobs) != 0 {
		t.Fatal("more than one in-flight edit for a message")
	}
	pool.Complete(Outcome{Job: old[0]})
	if session.DeliveredRevision == session.DesiredRevision {
		t.Fatal("old completion marked new revision delivered")
	}
	newest := pool.Claim(3)
	if len(newest) != 1 || newest[0].View.Text[0].Text != "new" {
		t.Fatal("latest screen lost")
	}
	pool.Complete(Outcome{
		Job: newest[0],
		Err: &EditError{RetryAfter: time.Minute},
	})
	if jobs := pool.Claim(3); len(jobs) != 0 {
		t.Fatal("ignored retry-after")
	}
	now = now.Add(time.Minute)
	retry := pool.Claim(3)
	if len(retry) != 1 {
		t.Fatal("retry not due")
	}
	pool.Complete(Outcome{
		Job: retry[0],
		Err: &EditError{NotModified: true},
	})
	pool.Stage(session, core.View{Text: core.Literal("new")})
	if jobs := pool.Claim(3); len(jobs) != 0 {
		t.Fatal("unchanged representation sent again")
	}
	pool.Stage(session, core.View{Text: core.Literal("deleted")})
	job := pool.Claim(3)[0]
	pool.Complete(Outcome{
		Job: job,
		Err: &EditError{Permanent: true},
	})
	if pool.Active(job) || len(pool.pool) != 0 {
		t.Fatal("permanent failure retained subscription")
	}
}

func TestExpiredAndEvictedClaimsAreCanceled(t *testing.T) {
	now := time.Unix(100, 0)
	i := 0
	pool, _ := New(time.Minute, 1, func() time.Time {
		return now
	}, func() (string, error) {
		i++
		return fmt.Sprint(i), nil
	})
	scope := core.Scope{
		Group: -1,
		Actor: 1,
	}
	first, _ := pool.Create("inventory", scope, 1)
	_, _ = pool.Bind(first.Token, scope, 1)
	pool.Stage(first, core.View{Text: core.Literal("first")})
	claim := pool.Claim(1)[0]
	second, _ := pool.Create("inventory", scope, 1)
	if pool.Active(claim) {
		t.Fatal("evicted menu claim remained active")
	}
	_, _ = pool.Bind(second.Token, scope, 2)
	pool.Stage(second, core.View{Text: core.Literal("second")})
	claim = pool.Claim(1)[0]
	now = now.Add(time.Minute)
	if pool.Active(claim) {
		t.Fatal("expired menu claim remained active")
	}
	pool.Complete(Outcome{Job: claim})
	if len(pool.pool) != 0 {
		t.Fatal("late completion recreated an expired subscription")
	}
}

func TestCloseOrdersTerminalDeliveryAndRejectsCallbacks(t *testing.T) {
	now := time.Unix(100, 0)
	pool, err := New(time.Hour, 3, func() time.Time {
		return now
	}, func() (string, error) {
		return "closing", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := core.Scope{
		Group: -1,
		Actor: 1,
	}
	session, err := pool.Create("craft", scope, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Bind(session.Token, scope, 1); err != nil {
		t.Fatal(err)
	}
	refs, err := pool.Actions(session, []Action{{
		Kind:  "confirm",
		Quote: "accepted",
	}})
	if err != nil {
		t.Fatal(err)
	}
	pool.AcceptConfirmation(session, "accepted")
	pool.Stage(session, core.View{Text: core.Literal("pending")})
	old := pool.Claim(1)[0]
	pool.Close(session, core.View{Text: core.Literal("closed")})
	if _, _, err = pool.Resolve(refs[0], scope, 1); err == nil {
		t.Fatal("closing session accepted callback")
	}
	if len(pool.Claim(1)) != 0 {
		t.Fatal("terminal edit raced pending delivery")
	}
	pool.Complete(Outcome{Job: old})
	terminal := pool.Claim(1)
	if len(terminal) != 1 || terminal[0].View.Text[0].Text != "closed" || len(terminal[0].View.Rows) != 0 {
		t.Fatal("terminal view lost")
	}
	pool.Complete(Outcome{Job: terminal[0], Err: &EditError{RetryAfter: time.Minute}})
	pool.Invalidate(Dependency{
		Kind:  "group",
		Group: -1,
	})
	if len(pool.Dirty()) != 0 || len(pool.Claim(1)) != 0 {
		t.Fatal("invalidation or failed terminal edit bypassed backoff")
	}
	now = now.Add(time.Minute)
	jobs := pool.Claim(1)
	if len(jobs) != 1 {
		t.Fatal("terminal delivery was not retried")
	}
	pool.Complete(Outcome{Job: jobs[0]})
	if pool.Contains(session.Token) || len(pool.Claim(1)) != 0 {
		t.Fatal("delivered closed menu remained active")
	}
}
