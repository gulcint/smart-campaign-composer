package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/health"
)

type pingFunc func(ctx context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

// dbErrText imitates a driver error carrying internals that must never reach the response body.
const dbErrText = "dial tcp 10.0.0.5:5432: connect: connection refused (user=scc_app password=hunter2)"

// REQ-TRK-03
// GET /healthz is 200 {"status":"ok"} whenever the process is up.
func TestHealthz_AlwaysOK(t *testing.T) {
	t.Parallel()
	rec := serve(t, health.Healthz(), "/healthz")
	assertJSON(t, rec, http.StatusOK, map[string]any{"status": "ok"})
}

// REQ-TRK-03
// GET /readyz is 200 when the database ping succeeds and 503 when it fails or exceeds 1s;
// the body never carries the database error text.
func TestReadyz_ReflectsDatabaseReachability(t *testing.T) {
	t.Parallel()
	ready := map[string]any{"status": "ready", "checks": map[string]any{"database": "ok"}}
	notReady := map[string]any{"status": "not_ready", "checks": map[string]any{"database": "unavailable"}}

	tests := []struct {
		name       string
		ping       pingFunc
		wantStatus int
		wantBody   map[string]any
	}{
		{
			name:       "reachable database is ready",
			ping:       func(context.Context) error { return nil },
			wantStatus: http.StatusOK,
			wantBody:   ready,
		},
		{
			name:       "unreachable database is not ready",
			ping:       func(context.Context) error { return errors.New(dbErrText) },
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   notReady,
		},
		{
			name: "ping that does not answer within the 1s timeout is not ready",
			ping: func(ctx context.Context) error {
				<-ctx.Done() // returns only because Readyz bounds the ping with a timeout
				return ctx.Err()
			},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   notReady,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := serve(t, health.Readyz(tc.ping), "/readyz")
			assertJSON(t, rec, tc.wantStatus, tc.wantBody)
			body := rec.Body.String()
			for _, leak := range []string{"connection refused", "hunter2", "10.0.0.5", "context deadline exceeded"} {
				if strings.Contains(body, leak) {
					t.Fatalf("body leaks %q: %s", leak, body)
				}
			}
		})
	}
}

// REQ-TRK-03
// Readyz pings with a bounded context (1s timeout, metrics.md §3).
func TestReadyz_PingHasDeadline(t *testing.T) {
	t.Parallel()
	hasDeadline := make(chan bool, 1)
	h := health.Readyz(pingFunc(func(ctx context.Context) error {
		_, ok := ctx.Deadline()
		hasDeadline <- ok
		return nil
	}))
	serve(t, h, "/readyz")
	select {
	case ok := <-hasDeadline:
		if !ok {
			t.Fatal("Ping context has no deadline, want a 1s timeout")
		}
	default:
		t.Fatal("Readyz did not call Ping")
	}
}

func serve(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec
}

func assertJSON(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantBody map[string]any) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, wantStatus, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not JSON (%v): %s", err, rec.Body.String())
	}
	if diff := cmp.Diff(wantBody, got); diff != "" {
		t.Fatalf("body mismatch (-want +got):\n%s", diff)
	}
}
