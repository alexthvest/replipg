package testsupport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// TelegramResponse describes one response returned by FakeTelegram.
type TelegramResponse struct {
	StatusCode          int
	Header              http.Header
	Body                []byte
	WaitForCancellation bool
	DropConnection      bool
}

// TelegramRequest is an immutable snapshot of a request received by
// FakeTelegram.
type TelegramRequest struct {
	Method   string
	Path     string
	RawQuery string
	Header   http.Header
	Body     []byte
}

// FakeTelegram is a local Bot API HTTP service with ordered programmable
// responses and recorded requests. It never contacts Telegram.
type FakeTelegram struct {
	mu        sync.Mutex
	responses []TelegramResponse
	requests  []TelegramRequest
	changed   chan struct{}
	server    *httptest.Server
	closeOnce sync.Once
}

// NewFakeTelegram starts a local fake service with the supplied responses.
func NewFakeTelegram(responses ...TelegramResponse) *FakeTelegram {
	fake := &FakeTelegram{
		responses: cloneResponses(responses),
		changed:   make(chan struct{}),
	}
	fake.server = httptest.NewServer(fake)
	return fake
}

// URL returns the fake service's base URL.
func (f *FakeTelegram) URL() string {
	return f.server.URL
}

// Client returns an HTTP client configured for the fake service.
func (f *FakeTelegram) Client() *http.Client {
	return f.server.Client()
}

// Enqueue appends responses to the ordered outcome queue.
func (f *FakeTelegram) Enqueue(responses ...TelegramResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.responses = append(f.responses, cloneResponses(responses)...)
}

// Requests returns independent copies of all recorded requests.
func (f *FakeTelegram) Requests() []TelegramRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	requests := make([]TelegramRequest, len(f.requests))
	for i, request := range f.requests {
		requests[i] = cloneRequest(request)
	}
	return requests
}

// WaitForRequests blocks until at least count requests have been recorded or
// ctx is done.
func (f *FakeTelegram) WaitForRequests(ctx context.Context, count int) error {
	for {
		f.mu.Lock()
		if len(f.requests) >= count {
			f.mu.Unlock()
			return nil
		}
		changed := f.changed
		f.mu.Unlock()

		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close stops the fake service and closes its client connections.
func (f *FakeTelegram) Close() {
	f.closeOnce.Do(func() {
		f.server.CloseClientConnections()
		f.server.Close()
	})
}

// ServeHTTP records a request and returns the next programmed response.
func (f *FakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "could not record request", http.StatusInternalServerError)
		return
	}
	if err := r.Body.Close(); err != nil {
		http.Error(w, "could not close request", http.StatusInternalServerError)
		return
	}

	request := TelegramRequest{
		Method:   r.Method,
		Path:     r.URL.Path,
		RawQuery: r.URL.RawQuery,
		Header:   r.Header.Clone(),
		Body:     append([]byte(nil), body...),
	}

	f.mu.Lock()
	f.requests = append(f.requests, request)
	var response TelegramResponse
	if len(f.responses) > 0 {
		response = f.responses[0]
		f.responses = f.responses[1:]
	} else {
		response = TelegramResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       []byte(`{"ok":false,"description":"no fake response programmed"}`),
		}
	}
	close(f.changed)
	f.changed = make(chan struct{})
	f.mu.Unlock()

	if response.WaitForCancellation {
		<-r.Context().Done()
		return
	}
	if response.DropConnection {
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "connection dropping is unsupported", http.StatusInternalServerError)
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		_ = connection.Close()
		return
	}
	for name, values := range response.Header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	statusCode := response.StatusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	w.WriteHeader(statusCode)
	_, _ = w.Write(response.Body)
}

func cloneResponses(responses []TelegramResponse) []TelegramResponse {
	clones := make([]TelegramResponse, len(responses))
	for i, response := range responses {
		clones[i] = TelegramResponse{
			StatusCode:          response.StatusCode,
			Header:              response.Header.Clone(),
			Body:                append([]byte(nil), response.Body...),
			WaitForCancellation: response.WaitForCancellation,
			DropConnection:      response.DropConnection,
		}
	}
	return clones
}

func cloneRequest(request TelegramRequest) TelegramRequest {
	return TelegramRequest{
		Method:   request.Method,
		Path:     request.Path,
		RawQuery: request.RawQuery,
		Header:   request.Header.Clone(),
		Body:     append([]byte(nil), request.Body...),
	}
}
