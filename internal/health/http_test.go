package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type countsFunc func(context.Context) (WorkCounts, error)

func (f countsFunc) Snapshot(ctx context.Context) (WorkCounts, error) { return f(ctx) }
func TestProbeCodesAndPrivacy(t *testing.T) {
	s := NewState()
	for _, dep := range []Dependency{Storage, Telegram, Intake, Outbox, Scheduler} {
		_ = s.SetDependency(dep, true)
	}
	counts := WorkCounts{Deliveries: DeliveryCounts{ReconciliationRequired: 3}, Jobs: JobCounts{Cancelled: 2}}
	handler := NewHandler(s, countsFunc(func(context.Context) (WorkCounts, error) { return counts, nil }))
	for _, phase := range []Lifecycle{Starting, Recovering, Ready, Draining, Stopped} {
		_ = s.SetLifecycle(phase)
		for _, path := range []string{"/livez", "/readyz", "/statusz"} {
			t.Run(string(phase)+path, func(t *testing.T) {
				rr := httptest.NewRecorder()
				handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, path, nil))
				want := http.StatusOK
				if phase == Stopped || (path == "/readyz" && phase != Ready) {
					want = http.StatusServiceUnavailable
				}
				if rr.Code != want {
					t.Fatalf("code=%d want=%d body=%s", rr.Code, want, rr.Body.String())
				}
				if rr.Header().Get("Content-Type") != "application/json" {
					t.Fatal("expected JSON")
				}
				var body map[string]json.RawMessage
				if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if path == "/statusz" {
					var got WorkCounts
					if err := json.Unmarshal(body["work"], &got); err != nil || got != counts {
						t.Fatalf("work=%+v err=%v", got, err)
					}
					if string(body["deployment_mode"]) != `"single_instance"` || string(body["multi_instance_supported"]) != "false" {
						t.Fatalf("mode=%s", rr.Body.String())
					}
				} else if _, ok := body["work"]; ok {
					t.Fatal("probe exposed work")
				}
			})
		}
	}
	_ = s.SetLifecycle(Ready)
	_ = s.SetDependency(Storage, false)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rr.Code != 503 || !strings.Contains(rr.Body.String(), "dependency_unavailable") {
		t.Fatalf("dependency failure=%d %s", rr.Code, rr.Body.String())
	}
}
func TestStatusFailureIsSafe(t *testing.T) {
	s := NewState()
	calls := 0
	handler := NewHandler(s, countsFunc(func(context.Context) (WorkCounts, error) {
		calls++
		return WorkCounts{Jobs: JobCounts{Pending: 99}}, errors.New("secret token private-content /private/path chat-123")
	}))
	for _, path := range []string{"/livez", "/readyz"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if calls != 0 {
		t.Fatal("simple probes touched durable storage")
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/statusz", nil))
	if rr.Code != 503 || strings.Contains(rr.Body.String(), "secret") || strings.Contains(rr.Body.String(), "99") || !strings.Contains(rr.Body.String(), "work_status_unavailable") {
		t.Fatalf("unsafe status=%d %s", rr.Code, rr.Body.String())
	}
	for _, request := range []*http.Request{httptest.NewRequest(http.MethodPost, "/statusz", nil), httptest.NewRequest(http.MethodGet, "/unknown", nil)} {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, request)
		want := 404
		if request.Method == http.MethodPost {
			want = 405
		}
		if rr.Code != want {
			t.Fatalf("method/path code=%d want=%d", rr.Code, want)
		}
	}
	if calls != 1 {
		t.Fatal("unsupported request queried storage")
	}
}
