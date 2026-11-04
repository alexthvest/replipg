package telegram

import (
	"context"
	"encoding/json"
	"github.com/alexthvest/replipg/internal/runtimecontract"
	"github.com/alexthvest/replipg/internal/testsupport"
	"testing"
)

func TestSetGroupCommands(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), response(`{"ok":true,"result":true}`))
	defer fake.Close()
	c := newTestClient(t, fake)
	declarations := []runtimecontract.CommandDeclaration{{Name: "character", Description: "Inspect character", Ephemeral: true}}
	for range 2 {
		if err := c.SetGroupCommands(context.Background(), declarations); err != nil {
			t.Fatal(err)
		}
	}
	requests := fake.Requests()
	if len(requests) != 3 {
		t.Fatal("requests")
	}
	for _, request := range requests[1:] {
		if request.Path != "/bot42:secret/setMyCommands" {
			t.Fatal(request.Path)
		}
		var body struct {
			Commands []struct {
				Command     string `json:"command"`
				IsEphemeral bool   `json:"is_ephemeral"`
			}
			Scope struct {
				Type string `json:"type"`
			}
			LanguageCode string `json:"language_code"`
		}
		fields := requestFields(t, request)
		if err := json.Unmarshal([]byte(fields["commands"]), &body.Commands); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(fields["scope"]), &body.Scope); err != nil {
			t.Fatal(err)
		}
		body.LanguageCode = fields["language_code"]
		if len(body.Commands) != 1 || body.Commands[0].Command != "character" || !body.Commands[0].IsEphemeral || body.Scope.Type != "all_group_chats" || body.LanguageCode != "" {
			t.Fatalf("unexpected body: %s", request.Body)
		}
	}
}
func TestSetGroupCommandsFailures(t *testing.T) {
	for _, tc := range []struct {
		body string
		kind ErrorKind
	}{
		{`{"ok":true,"result":false}`, ErrorIndeterminate},
		{`{"ok":true,"result":"invalid"}`, ErrorIndeterminate},
		{`{"ok":true}`, ErrorIndeterminate},
		{`{"ok":false,"error_code":400,"description":"private"}`, ErrorValidation},
		{`{"ok":false,"error_code":429,"parameters":{"retry_after":2}}`, ErrorRateLimit},
	} {
		fake := testsupport.NewFakeTelegram(response(identity), response(tc.body))
		c := newTestClient(t, fake)
		_ = assertKind(t, c.SetGroupCommands(context.Background(), nil), tc.kind)
		fake.Close()
	}
	fake := testsupport.NewFakeTelegram(response(identity))
	defer fake.Close()
	c := newTestClient(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = assertKind(t, c.SetGroupCommands(ctx, nil), ErrorCancellation)
	_ = assertKind(t, c.SetGroupCommands(context.Background(), []runtimecontract.CommandDeclaration{{Name: "/bad"}}), ErrorValidation)
	if len(fake.Requests()) != 1 {
		t.Fatal("invalid input made request")
	}
}
