package health

import (
	"context"
	"encoding/json"
	"net/http"
)

// WorkStatusSource supplies safe aggregate counts for the status endpoint.
type WorkStatusSource interface {
	Snapshot(context.Context) (WorkCounts, error)
}

// NewHandler serves read-only liveness, readiness, and aggregate status probes.
// The caller owns HTTP server timeouts and lifecycle. Simple probes never read storage.
func NewHandler(state *State, work WorkStatusSource) http.Handler {
	if state == nil {
		state = NewState()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/livez", "/readyz", "/statusz":
		default:
			http.NotFound(w, r)
			return
		}

		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		snapshot := state.Snapshot()
		code := http.StatusOK
		var body any

		switch r.URL.Path {
		case "/livez":
			if !snapshot.Live {
				code = http.StatusServiceUnavailable
			}
			body = struct {
				Lifecycle Lifecycle `json:"lifecycle"`
			}{snapshot.Lifecycle}
		case "/readyz":
			if !snapshot.Ready {
				code = http.StatusServiceUnavailable
			}
			body = struct {
				Lifecycle Lifecycle  `json:"lifecycle"`
				Reason    SafeReason `json:"reason,omitempty"`
			}{snapshot.Lifecycle, snapshot.Reason}
		case "/statusz":
			if !snapshot.Live {
				code = http.StatusServiceUnavailable
			}
			result := struct {
				Snapshot
				Work *WorkCounts `json:"work,omitempty"`
			}{Snapshot: snapshot}
			var counts WorkCounts
			var err error

			if work == nil {
				err = ErrWorkStatusUnavailable
			} else {
				counts, err = work.Snapshot(r.Context())
			}
			if err != nil {
				code = http.StatusServiceUnavailable
				result.Ready = false
				result.Reason = "work_status_unavailable"
			} else {
				result.Work = &counts
			}
			body = result
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(body)
	})
}
