package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/alexthvest/replipg/internal/testsupport"
)

const identity = `{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"test"}}`

func response(body string) testsupport.TelegramResponse {
	return testsupport.TelegramResponse{Body: []byte(body)}
}

func newTestClient(t *testing.T, fake *testsupport.FakeTelegram) *Client {
	t.Helper()
	client, err := NewClient(context.Background(), ClientConfig{Token: "42:secret", BaseURL: fake.URL(), HTTPClient: fake.Client(), RequestTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	return client
}

func assertKind(t *testing.T, err error, kind ErrorKind) *APIError {
	t.Helper()
	var safe *APIError

	if !errors.As(err, &safe) || safe.Kind != kind {
		t.Fatalf("got %v, want %s", err, kind)
	}

	if strings.Contains(fmt.Sprintf("%+v", err), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}

	return safe
}

func TestIdentityAndRawIntake(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":true,"result":true}`), response(`{"ok":true,"result":[{"update_id":7,"message":"malformed"}]}`))
	defer fake.Close()

	c := newTestClient(t, fake)
	if err := c.DeleteWebhook(context.Background(), false); err != nil {
		t.Fatal(err)
	}

	updates, err := c.GetUpdates(context.Background(), GetUpdatesRequest{Offset: 7, Limit: 1, Timeout: 0, AllowedUpdates: []string{"message", "callback_query"}})
	if err != nil || len(updates) != 1 || !strings.Contains(string(updates[0]), `"update_id":7`) {
		t.Fatalf("updates=%s err=%v", updates, err)
	}

	requests := fake.Requests()

	if len(requests) != 3 || requests[0].Path != "/bot42:secret/getMe" {
		t.Fatalf("requests=%v", requests)
	}

	if strings.Contains(string(requests[1].Body), "true") {
		t.Fatal("deleteWebhook did not preserve backlog")
	}
}

func TestSafeAPICategories(t *testing.T) {
	for _, tc := range []struct {
		code int
		kind ErrorKind
	}{{400, ErrorValidation}, {401, ErrorAuthorization}, {403, ErrorAuthorization}, {404, ErrorPermanent}, {409, ErrorConflict}, {429, ErrorRateLimit}, {500, ErrorTransient}} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			fake := testsupport.NewFakeTelegram(response(identity), response(fmt.Sprintf(`{"ok":false,"error_code":%d,"description":"secret raw content","parameters":{"retry_after":12}}`, tc.code)))
			defer fake.Close()

			c := newTestClient(t, fake)
			err := c.DeleteWebhook(context.Background(), false)
			safe := assertKind(t, err, tc.kind)

			if tc.code == 429 && safe.RetryAfter != 12*time.Second {
				t.Fatalf("delay=%v", safe.RetryAfter)
			}

			if errors.Unwrap(safe) != nil {
				t.Fatal("retained unsafe upstream cause")
			}
		})
	}
}

func TestIdentityFailureAndConfiguration(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(`{"ok":false,"error_code":401,"description":"secret"}`))
	defer fake.Close()

	_, err := NewClient(context.Background(), ClientConfig{Token: "42:secret", BaseURL: fake.URL(), HTTPClient: fake.Client(), RequestTimeout: time.Second})
	_ = assertKind(t, err, ErrorAuthorization)
	for _, config := range []ClientConfig{{}, {Token: "42:secret"}, {Token: "42:secret", RequestTimeout: time.Second, BaseURL: ":secret"}} {
		_, err := NewClient(context.Background(), config)
		_ = assertKind(t, err, ErrorValidation)
	}
}

func TestTransportUncertainty(t *testing.T) {
	for _, outcome := range []testsupport.TelegramResponse{{DropConnection: true}, response("secret invalid JSON"), response(`{"ok":true,"result":"invalid boolean"}`)} {
		fake := testsupport.NewFakeTelegram(response(identity), outcome)
		c := newTestClient(t, fake)
		err := c.DeleteWebhook(context.Background(), false)
		_ = assertKind(t, err, ErrorIndeterminate)
		fake.Close()
	}
}

func TestDeleteWebhookRequiresConfirmedTrue(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"false", `{"ok":true,"result":false}`},
		{"null", `{"ok":true,"result":null}`},
		{"missing", `{"ok":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := testsupport.NewFakeTelegram(response(identity), response(tc.body))
			defer fake.Close()

			c := newTestClient(t, fake)
			safe := assertKind(t, c.DeleteWebhook(context.Background(), false), ErrorIndeterminate)

			if safe.RetryAfter != 0 || errors.Unwrap(safe) != nil {
				t.Fatal("unconfirmed cleanup retained a retry delay or upstream cause")
			}

			if len(fake.Requests()) != 2 {
				t.Fatal("client retried unconfirmed webhook cleanup")
			}
		})
	}
}

func TestCancellationAndDeadline(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity))
	defer fake.Close()

	c := newTestClient(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.DeleteWebhook(ctx, false)
	_ = assertKind(t, err, ErrorCancellation)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation")
	}

	if len(fake.Requests()) != 1 {
		t.Fatal("cancelled call sent")
	}

	fake.Enqueue(testsupport.TelegramResponse{WaitForCancellation: true})
	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	_, err = c.GetUpdates(ctx, GetUpdatesRequest{Limit: 1})
	_ = assertKind(t, err, ErrorCancellation)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("lost deadline")
	}
}

func TestBoundedIdentity(t *testing.T) {
	fake := testsupport.NewFakeTelegram(testsupport.TelegramResponse{WaitForCancellation: true})
	defer fake.Close()

	_, err := NewClient(context.Background(), ClientConfig{Token: "42:secret", BaseURL: fake.URL(), HTTPClient: fake.Client(), RequestTimeout: 25 * time.Millisecond})
	_ = assertKind(t, err, ErrorCancellation)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("unbounded identity")
	}
}

func TestMutationCancellationAfterTransmission(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), testsupport.TelegramResponse{WaitForCancellation: true})
	defer fake.Close()

	c := newTestClient(t, fake)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.DeleteWebhook(ctx, false) }()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), time.Second)
	defer waitCancel()

	if err := fake.WaitForRequests(waitCtx, 2); err != nil {
		cancel()
		t.Fatal(err)
	}

	cancel()
	_ = assertKind(t, <-done, ErrorIndeterminate)
}

func TestHTTPStatusFailure(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), testsupport.TelegramResponse{StatusCode: http.StatusServiceUnavailable, Body: []byte("secret proxy error")})
	defer fake.Close()

	c := newTestClient(t, fake)
	_, err := c.GetUpdates(context.Background(), GetUpdatesRequest{Limit: 1})
	_ = assertKind(t, err, ErrorTransient)
}

func TestContradictoryAcceptanceRequiresReconciliation(t *testing.T) {
	for _, status := range []int{302, 400, 401, 403, 409, 429, 500, 503} {
		for _, errorCode := range []int{0, 429} {
			t.Run(fmt.Sprintf("status_%d/error_code_%d", status, errorCode), func(t *testing.T) {
				outcome := testsupport.TelegramResponse{
					StatusCode: status,
					Body:       []byte(fmt.Sprintf(`{"ok":true,"result":true,"error_code":%d,"parameters":{"retry_after":12}}`, errorCode)),
				}
				fake := testsupport.NewFakeTelegram(response(identity), outcome, outcome)
				defer fake.Close()

				c := newTestClient(t, fake)
				safe := assertKind(t, c.DeleteWebhook(context.Background(), false), ErrorIndeterminate)

				if safe.RetryAfter != 0 {
					t.Fatal("contradictory acceptance offered an automatic retry delay")
				}

				_, err := c.GetUpdates(context.Background(), GetUpdatesRequest{Limit: 1})
				_ = assertKind(t, err, ErrorTransient)

				if len(fake.Requests()) != 3 {
					t.Fatal("client retried a contradictory response")
				}
			})
		}
	}
}

func TestUnsafeRetryDelaysAreNotShortened(t *testing.T) {
	for _, delay := range []string{"-1", "9223372036854775807", "999999999999999999999999999999999"} {
		fake := testsupport.NewFakeTelegram(response(identity), response(`{"ok":false,"error_code":429,"parameters":{"retry_after":`+delay+`}}`))
		c := newTestClient(t, fake)
		_ = assertKind(t, c.DeleteWebhook(context.Background(), false), ErrorPermanent)
		fake.Close()
	}
}

func TestInvalidIdentityAndRejectedDrop(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(`{"ok":true,"result":{"id":42,"is_bot":false}}`))
	defer fake.Close()

	_, err := NewClient(context.Background(), ClientConfig{Token: "42:secret", BaseURL: fake.URL(), HTTPClient: fake.Client(), RequestTimeout: time.Second})
	_ = assertKind(t, err, ErrorPermanent)
	fake.Enqueue(response(identity))
	c := newTestClient(t, fake)
	_ = assertKind(t, c.DeleteWebhook(context.Background(), true), ErrorValidation)

	if len(fake.Requests()) != 2 {
		t.Fatal("unsafe webhook deletion attempted")
	}
}

type rejectingTransport struct{}

func (rejectingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("secret transport URL and content")
}

func TestFailureBeforeTransmissionIsSafeToRetry(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity))
	defer fake.Close()

	c := newTestClient(t, fake)
	c.transport.client.Transport = rejectingTransport{}
	_ = assertKind(t, c.DeleteWebhook(context.Background(), false), ErrorTransient)
}

func TestOversizedResponseIsBoundedAndUncertain(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), response(strings.Repeat("secret", maxResponseBytes/6+1)))
	defer fake.Close()

	c := newTestClient(t, fake)
	_ = assertKind(t, c.DeleteWebhook(context.Background(), false), ErrorIndeterminate)
}

func TestRedirectIsNotFollowed(t *testing.T) {
	fake := testsupport.NewFakeTelegram(response(identity), testsupport.TelegramResponse{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": []string{"/bot42:secret/getMe"}}})
	defer fake.Close()

	c := newTestClient(t, fake)
	_ = assertKind(t, c.DeleteWebhook(context.Background(), false), ErrorIndeterminate)

	if len(fake.Requests()) != 2 {
		t.Fatal("redirect was followed")
	}
}

func TestMalformedPollResultIsNotAnEmptySuccess(t *testing.T) {
	for _, body := range []string{`{"ok":true}`, `{"ok":true,"result":null}`, `{"ok":true,"result":{}}`, `{"ok":true,"result":[{"update_id":1},{"update_id":2}]}`} {
		fake := testsupport.NewFakeTelegram(response(identity), response(body))
		c := newTestClient(t, fake)
		_, err := c.GetUpdates(context.Background(), GetUpdatesRequest{Limit: 1})
		_ = assertKind(t, err, ErrorTransient)
		fake.Close()
	}
}
