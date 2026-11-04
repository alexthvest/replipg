// Package telegram adapts the Telegram Bot API without exposing transport errors.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"
)

// ErrorKind identifies an operational outcome without retaining Telegram text.
type ErrorKind string

const (
	ErrorValidation    ErrorKind = "validation"
	ErrorAuthorization ErrorKind = "authorization"
	ErrorConflict      ErrorKind = "conflict"
	ErrorRateLimit     ErrorKind = "rate_limit"
	ErrorTransient     ErrorKind = "transient"
	ErrorPermanent     ErrorKind = "permanent"
	ErrorCancellation  ErrorKind = "cancellation"
	ErrorIndeterminate ErrorKind = "indeterminate"
)

// APIError contains only safe classifications; upstream text and URLs are discarded.
type APIError struct {
	Kind       ErrorKind
	RetryAfter time.Duration
	cause      error
}

func (e *APIError) Error() string { return "telegram: " + string(e.Kind) }
func (e *APIError) Unwrap() error { return e.cause }

// ClientConfig specifies the shared transport and operation deadline.
type ClientConfig struct {
	Token          string
	BaseURL        string
	HTTPClient     *http.Client
	RequestTimeout time.Duration
}

// Client owns one pinned library instance shared by intake and delivery.
type Client struct {
	botID          int64
	bot            *bot.Bot
	transport      *safeHTTP
	baseURL, token string
	timeout        time.Duration
}

const maxResponseBytes = 4 << 20

// NewClient verifies the bot identity using the caller's context and a bounded deadline.
func NewClient(ctx context.Context, config ClientConfig) (*Client, error) {
	if strings.TrimSpace(config.Token) == "" || config.RequestTimeout <= 0 {
		return nil, &APIError{Kind: ErrorValidation}
	}
	base := config.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, &APIError{Kind: ErrorValidation}
	}
	// Clone caller configuration; redirects may disclose the token or replay a send.
	httpClient := http.Client{}
	if config.HTTPClient != nil {
		httpClient = *config.HTTPClient
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	transport := &safeHTTP{client: &httpClient}
	pinned, err := bot.New(config.Token, bot.WithSkipGetMe(), bot.WithServerURL(strings.TrimRight(base, "/")), bot.WithHTTPClient(config.RequestTimeout, transport), bot.WithErrorsHandler(func(error) {}))
	if err != nil {
		return nil, &APIError{Kind: ErrorValidation}
	}
	c := &Client{bot: pinned, transport: transport, baseURL: strings.TrimRight(base, "/"), token: config.Token, timeout: config.RequestTimeout}
	err = c.call(ctx, false, func(ctx context.Context) error {
		me, err := pinned.GetMe(ctx)
		if err != nil {
			return err
		}
		if me == nil || me.ID <= 0 || !me.IsBot {
			return &APIError{Kind: ErrorPermanent}
		}
		c.botID = me.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

type requestState struct {
	mutating bool
	sent     atomic.Bool
}
type requestStateKey struct{}

func (c *Client) call(ctx context.Context, mutating bool, fn func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return &APIError{Kind: ErrorCancellation, cause: err}
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	state := &requestState{mutating: mutating}
	ctx = context.WithValue(ctx, requestStateKey{}, state)
	err := fn(ctx)
	if err == nil {
		return nil
	}
	var safe *APIError
	if errors.As(err, &safe) {
		return safe
	}
	return transportError(ctx, state)
}
func transportError(ctx context.Context, state *requestState) *APIError {
	kind := ErrorTransient
	if ctx.Err() != nil {
		kind = ErrorCancellation
	}
	if state != nil && state.mutating && state.sent.Load() {
		kind = ErrorIndeterminate
	}
	return &APIError{Kind: kind, cause: ctx.Err()}
}

// GetUpdatesRequest supplies completion-controlled polling parameters.
type GetUpdatesRequest struct {
	Offset         int64    `json:"offset"`
	Limit          int      `json:"limit"`
	Timeout        int      `json:"timeout"`
	AllowedUpdates []string `json:"allowed_updates"`
}

// GetUpdates preserves each raw update so its ID can be decoded before its payload.
func (c *Client) GetUpdates(ctx context.Context, request GetUpdatesRequest) ([]json.RawMessage, error) {
	if request.Offset < 0 || request.Limit != 1 || request.Timeout < 0 || request.Timeout > int(c.timeout/time.Second) {
		return nil, &APIError{Kind: ErrorValidation}
	}
	var updates []json.RawMessage
	err := c.call(ctx, false, func(ctx context.Context) error {
		body, err := json.Marshal(request)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/bot"+c.token+"/getUpdates", bytes.NewReader(body))
		if err != nil {
			return &APIError{Kind: ErrorValidation}
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := c.transport.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		var envelope struct {
			Result []json.RawMessage `json:"result"`
		}
		if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
			return &APIError{Kind: ErrorTransient}
		}
		if envelope.Result == nil || len(envelope.Result) > request.Limit {
			return &APIError{Kind: ErrorTransient}
		}
		updates = envelope.Result
		return nil
	})
	return updates, err
}

// DeleteWebhook disables conflicting intake while preserving queued updates.
func (c *Client) DeleteWebhook(ctx context.Context, dropPending bool) error {
	if dropPending {
		return &APIError{Kind: ErrorValidation}
	}
	return c.call(ctx, true, func(ctx context.Context) error {
		deleted, err := c.bot.DeleteWebhook(ctx, &bot.DeleteWebhookParams{DropPendingUpdates: false})
		if err != nil {
			return err
		}
		if !deleted {
			// An OK envelope alone cannot confirm the startup prerequisite.
			return &APIError{Kind: ErrorIndeterminate}
		}
		return nil
	})
}

type safeHTTP struct{ client *http.Client }

func (s *safeHTTP) Do(req *http.Request) (*http.Response, error) {
	state, _ := req.Context().Value(requestStateKey{}).(*requestState)
	trace := &httptrace.ClientTrace{WroteHeaders: func() {
		if state != nil {
			state.sent.Store(true)
		}
	}}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	response, err := s.client.Do(req)
	if err != nil {
		return nil, transportError(req.Context(), state)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	_ = response.Body.Close()
	if err != nil {
		return nil, transportError(req.Context(), state)
	}
	var envelope struct {
		OK         *bool `json:"ok"`
		ErrorCode  int   `json:"error_code"`
		Parameters struct {
			RetryAfter json.Number `json:"retry_after"`
		} `json:"parameters"`
	}
	if len(body) > maxResponseBytes || json.Unmarshal(body, &envelope) != nil || envelope.OK == nil {
		if state != nil && state.mutating {
			return nil, &APIError{Kind: ErrorIndeterminate}
		}
		return nil, &APIError{Kind: ErrorTransient}
	}
	if *envelope.OK && (response.StatusCode < 200 || response.StatusCode >= 300) {
		// Conflicting acceptance evidence cannot establish that a mutation is safe to retry.
		if state != nil && state.mutating {
			return nil, &APIError{Kind: ErrorIndeterminate}
		}
		return nil, &APIError{Kind: ErrorTransient}
	}
	if !*envelope.OK {
		code := envelope.ErrorCode
		if code == 0 {
			code = response.StatusCode
		}
		safe := &APIError{Kind: ErrorPermanent}
		switch {
		case code == 400:
			safe.Kind = ErrorValidation
		case code == 401 || code == 403:
			safe.Kind = ErrorAuthorization
		case code == 409:
			safe.Kind = ErrorConflict
		case code == 429:
			safe.Kind = ErrorRateLimit
		case code >= 500:
			safe.Kind = ErrorTransient
		}
		if safe.Kind == ErrorRateLimit {
			seconds, err := envelope.Parameters.RetryAfter.Int64()
			if envelope.Parameters.RetryAfter == "" {
				seconds = 0
				err = nil
			}
			if err != nil || seconds < 0 || seconds > math.MaxInt64/int64(time.Second) {
				return nil, &APIError{Kind: ErrorPermanent}
			}
			safe.RetryAfter = time.Duration(seconds) * time.Second
		}
		return nil, safe
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	return response, nil
}
