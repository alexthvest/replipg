package testsupport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestFakeTelegramRecordsRequestsAndReturnsProgrammedOutcomes(t *testing.T) {
	t.Parallel()

	fake := NewFakeTelegram(
		TelegramResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":true,"result":[]}`)},
		TelegramResponse{StatusCode: http.StatusTooManyRequests, Body: []byte(`{"ok":false,"parameters":{"retry_after":7}}`)},
		TelegramResponse{StatusCode: http.StatusOK, Body: []byte(`{"ok":`)},
	)
	defer fake.Close()

	for _, want := range []struct {
		status int
		body   string
	}{
		{http.StatusOK, `{"ok":true,"result":[]}`},
		{http.StatusTooManyRequests, `{"ok":false,"parameters":{"retry_after":7}}`},
		{http.StatusOK, `{"ok":`},
	} {
		req, err := http.NewRequest(http.MethodPost, fake.URL()+"/bot-test/getUpdates", strings.NewReader(`{"offset":42}`))
		if err != nil {
			t.Fatalf("create request: %v", err)
		}

		req.Header.Set("Content-Type", "application/json")
		resp, err := fake.Client().Do(req)
		if err != nil {
			t.Fatalf("perform request: %v", err)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}

		if err := resp.Body.Close(); err != nil {
			t.Fatalf("close response: %v", err)
		}

		if resp.StatusCode != want.status || string(body) != want.body {
			t.Fatalf("response = (%d, %q), want (%d, %q)", resp.StatusCode, body, want.status, want.body)
		}
	}

	requests := fake.Requests()

	if len(requests) != 3 {
		t.Fatalf("recorded requests = %d, want 3", len(requests))
	}

	if requests[0].Method != http.MethodPost || requests[0].Path != "/bot-test/getUpdates" {
		t.Fatalf("first request = %#v", requests[0])
	}

	if got := requests[0].Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}

	if got := string(requests[0].Body); got != `{"offset":42}` {
		t.Fatalf("body = %q", got)
	}
}

func TestFakeTelegramInterruptedResponseFollowsRequestCancellation(t *testing.T) {
	t.Parallel()

	fake := NewFakeTelegram(TelegramResponse{WaitForCancellation: true})
	defer fake.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fake.URL()+"/bot-test/getUpdates", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	result := make(chan error, 1)
	go func() {
		_, requestErr := fake.Client().Do(req)
		result <- requestErr
	}()

	waitCtx, stopWaiting := context.WithTimeout(context.Background(), time.Second)
	defer stopWaiting()

	if err := fake.WaitForRequests(waitCtx, 1); err != nil {
		t.Fatalf("wait for recorded request: %v", err)
	}

	cancel()

	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("request returned %v, want context.Canceled", err)
	}
}

func TestFakeTelegramCanInterruptAConnection(t *testing.T) {
	t.Parallel()

	fake := NewFakeTelegram(TelegramResponse{DropConnection: true})
	defer fake.Close()

	resp, err := fake.Client().Get(fake.URL() + "/bot-test/getUpdates")
	if err == nil {
		_ = resp.Body.Close()
		t.Fatal("request unexpectedly succeeded after connection drop")
	}

	if got := len(fake.Requests()); got != 1 {
		t.Fatalf("recorded requests = %d, want 1", got)
	}
}

func TestFakeTelegramCopiesRecordedRequests(t *testing.T) {
	t.Parallel()

	fake := NewFakeTelegram(TelegramResponse{StatusCode: http.StatusOK})
	defer fake.Close()

	resp, err := fake.Client().Get(fake.URL() + "/bot-test/getMe")
	if err != nil {
		t.Fatalf("perform request: %v", err)
	}

	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close response: %v", err)
	}

	requests := fake.Requests()
	requests[0].Header.Set("Mutated", "true")
	requests[0].Body = append(requests[0].Body, 'x')
	if got := fake.Requests()[0].Header.Get("Mutated"); got != "" {
		t.Fatalf("recorded header was mutated: %q", got)
	}
}
