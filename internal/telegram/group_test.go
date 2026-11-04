package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
)

func TestVerifyGroupMember(t *testing.T) {
	for _, tc := range []struct {
		bot, actor string
		want       runtimecontract.MembershipStatus
	}{
		{"administrator", `{"status":"administrator","user":{"id":7,"is_bot":false}}`, runtimecontract.Administrator},
		{"creator", `{"status":"creator","user":{"id":7,"is_bot":false}}`, runtimecontract.Owner},
		{"administrator", `{"status":"member","user":{"id":7,"is_bot":false}}`, runtimecontract.Member},
		{"member", `{"status":"administrator","user":{"id":7,"is_bot":false}}`, runtimecontract.Unknown},
		{"administrator", `{"status":"administrator","user":{"id":8,"is_bot":false}}`, runtimecontract.Unknown},
		{"administrator", `{"status":"administrator","user":{"id":7,"is_bot":true}}`, runtimecontract.Unknown},
		{"administrator", `null`, runtimecontract.Unknown},
	} {
		t.Run(tc.bot+tc.actor, func(t *testing.T) {
			fake := testsupport.NewFakeTelegram(response(identity), response(fmt.Sprintf(`{"ok":true,"result":{"status":%q,"user":{"id":42,"is_bot":true}}}`, tc.bot)), response(`{"ok":true,"result":`+tc.actor+`}`))
			defer fake.Close()

			c := newTestClient(t, fake)
			result, err := c.VerifyGroupMember(context.Background(), -100, 7)
			if err != nil || result.Status != tc.want || result.CheckedAt.IsZero() {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}
}

func TestReplaceGroupMenuScopesAndFailures(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), response(`{"ok":true,"result":true}`), response(`{"ok":true,"result":true}`))
	defer fake.Close()

	c := newTestClient(t, fake)
	if err := c.ReplaceGroupMenu(context.Background(), -100, []runtimecontract.CommandDeclaration{{Name: "settings", Description: "Настройки", Ephemeral: true}}); err != nil {
		t.Fatal(err)
	}

	requests := fake.Requests()

	if len(requests) != 4 {
		t.Fatal(len(requests))
	}

	for i, r := range requests[1:] {
		fields := requestFields(t, r)

		if fields["scope"] != `{"type":"chat","chat_id":-100}` {
			t.Fatal(fields)
		}

		want := ""
		if i == 1 {
			want = "en"
		}
		if i == 2 {
			want = "ru"
		}

		if fields["language_code"] != want {
			t.Fatal(fields)
		}
	}

	fake.Enqueue(response(`{"ok":false,"error_code":429,"description":"secret","parameters":{"retry_after":2}}`))
	err := c.ReplaceGroupMenu(context.Background(), -100, nil)
	var safe *runtimecontract.MenuError

	if !errors.As(err, &safe) || safe.Kind != runtimecontract.RateLimited || safe.RetryAfter.Seconds() != 2 {
		t.Fatal(err)
	}
}

func TestGroupMenuClassificationsAndIncompleteCleanup(t *testing.T) {
	for _, tc := range []struct {
		body string
		kind runtimecontract.MenuFailureKind
	}{
		{`{"ok":false,"error_code":400,"description":"secret"}`, runtimecontract.Invalid},
		{`{"ok":false,"error_code":403,"description":"secret"}`, runtimecontract.Forbidden},
		{`{"ok":false,"error_code":500,"description":"secret"}`, runtimecontract.Transient},
		{`{"ok":true,"result":false}`, runtimecontract.Indeterminate},
		{`{"ok":true,"result":"bad"}`, runtimecontract.Indeterminate},
	} {
		for _, step := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("%d/%d", tc.kind, step), func(t *testing.T) {
				responses := []testsupport.TelegramResponse{response(identity)}
				for range step {
					responses = append(responses, response(`{"ok":true,"result":true}`))
				}

				responses = append(responses, response(tc.body))
				fake := testsupport.NewFakeTelegram(responses...)
				defer fake.Close()

				c := newTestClient(t, fake)
				err := c.ReplaceGroupMenu(context.Background(), -100, nil)
				var safe *runtimecontract.MenuError

				if !errors.As(err, &safe) || safe.Kind != tc.kind || strings.Contains(err.Error(), "secret") {
					t.Fatalf("unsafe classification %v", err)
				}

				if len(fake.Requests()) != step+2 {
					t.Fatal("continued after failure")
				}
			})
		}
	}
}

func TestGroupBoundariesCancellationAndInvalidInput(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity))
	defer fake.Close()

	c := newTestClient(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.VerifyGroupMember(ctx, -100, 7); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if err := c.ReplaceGroupMenu(ctx, -100, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if got, err := c.VerifyGroupMember(context.Background(), 100, 7); err != nil || got.Status != runtimecontract.Unknown {
		t.Fatalf("%v %v", got, err)
	}

	if err := c.ReplaceGroupMenu(context.Background(), 100, nil); err == nil {
		t.Fatal("accepted non-group")
	}

	if len(fake.Requests()) != 1 {
		t.Fatal("invalid request reached API")
	}
}

func TestMembershipFailuresAndReturnedBotIdentity(t *testing.T) {
	for _, body := range []string{
		`{"ok":true,"result":null}`,
		`{"ok":true,"result":{"status":"administrator","user":{"id":43,"is_bot":true}}}`,
		`{"ok":true,"result":{"status":"administrator","user":{"id":42,"is_bot":false}}}`,
		`{"ok":true,"result":{"status":"unrecognized","user":{"id":42,"is_bot":true}}}`,
		`{"ok":false,"error_code":403,"description":"secret"}`,
	} {
		fake := testsupport.NewFakeTelegram(response(identity), response(body))
		c := newTestClient(t, fake)
		got, err := c.VerifyGroupMember(context.Background(), -100, 7)
		if err != nil || got.Status != runtimecontract.Unknown {
			t.Fatalf("%+v %v", got, err)
		}

		if len(fake.Requests()) != 2 {
			t.Fatal("actor checked without sufficient bot rights")
		}

		fake.Close()
	}
}

func TestMembershipTimeoutRemainsUnauthorized(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), testsupport.TelegramResponse{WaitForCancellation: true})
	defer fake.Close()

	c := newTestClient(t, fake)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	got, err := c.VerifyGroupMember(ctx, -100, 7)
	if !errors.Is(err, context.DeadlineExceeded) || got.Status != runtimecontract.Unknown {
		t.Fatalf("%+v %v", got, err)
	}
}
