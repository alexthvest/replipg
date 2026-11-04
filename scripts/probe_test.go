package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestProbeReportsHTTPStatusWithoutFollowingRedirects(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != "/readyz" {
					t.Errorf("unexpected probe request: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(status)
			}))
			defer server.Close()
			t.Setenv("REPLIPG_HEALTH_ADDRESS", strings.TrimPrefix(server.URL, "http://"))
			want := 1
			if status == http.StatusOK {
				want = 0
			}
			if got := probe(nil); got != want {
				t.Errorf("probe exit = %d, want %d", got, want)
			}
			if got := requests.Load(); got != 1 {
				t.Errorf("requests = %d, want one without redirects", got)
			}
		})
	}
}
