package contract_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/test/contract"
	"github.com/gulcint/smart-campaign-composer/test/testenv/apispec"
)

// Hand-made responses: no server code is involved, only httptest and the E2E transport. They prove
// that strict validation (docs/contracts/README.md §11) really catches drift.

const (
	campID    = "0b7c1c2e-5a8f-4d2b-9c3e-1f2a3b4c5d6e"
	jsonCT    = "application/json"
	validCamp = `{"id":"` + campID + `","brief":"x","state":"draft","segment":null,"latest_content_version":null,` +
		`"approval":null,"sending_started_at":null,"completed_at":null,` +
		`"created_at":"2026-01-15T12:00:00Z","updated_at":"2026-01-15T12:00:00Z"}`
	notFoundBody   = `{"error":{"code":"campaign_not_found","message":"campaign not found","request_id":"r-1"}}`
	validationBody = `{"error":{"code":"validation_failed","message":"request validation failed",` +
		`"fields":[{"path":"state","code":"schema_violation","message":"unknown property"}],"request_id":"r-2"}}`
)

type reply struct {
	status int
	header map[string]string
	body   string
}

func serve(t *testing.T, r reply) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		for k, v := range r.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(r.status)
		_, _ = io.WriteString(w, r.body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func strictClient(srvURL string) (*http.Client, *apispec.Transport) {
	tr := &apispec.Transport{Strict: true, Scope: srvURL}
	return &http.Client{Transport: tr}, tr
}

// doVia sends one request, reads and closes the response body, and returns the status (0 on error).
func doVia(t *testing.T, c *http.Client, method, url, body string) (int, error) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, rd)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", jsonCT)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func js(extra ...string) map[string]string {
	h := map[string]string{"Content-Type": jsonCT, "X-Request-ID": "r-1"}
	for i := 0; i+1 < len(extra); i += 2 {
		h[extra[i]] = extra[i+1]
	}
	return h
}

// REQ-AI-01, REQ-AUD-05, REQ-TRK-03
// The strict transport reports a violation for an extra field, a wrong type, a missing required
// field, an unlisted status code, an error code the response does not allow, `fields` where it is
// forbidden, a wrong media type, a non-JSON body and an invalid request that got a 2xx; it accepts
// conforming exchanges (including an invalid request answered with 4xx) and records the operationId.
func TestStrictTransport_CatchesDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		method, path  string
		reqBody       string
		reply         reply
		wantViolation bool
		wantOp        string
	}{
		{name: "conforming health response passes", method: "GET", path: "/healthz",
			reply: reply{200, js(), `{"status":"ok"}`}, wantOp: "getHealthz"},
		{name: "extra field in a closed schema is a violation", method: "GET", path: "/healthz",
			reply: reply{200, js(), `{"status":"ok","uptime_seconds":3}`}, wantViolation: true, wantOp: "getHealthz"},
		{name: "wrong type is a violation", method: "GET", path: "/healthz",
			reply: reply{200, js(), `{"status":1}`}, wantViolation: true, wantOp: "getHealthz"},
		{name: "missing required field is a violation", method: "GET", path: "/healthz",
			reply: reply{200, js(), `{}`}, wantViolation: true, wantOp: "getHealthz"},
		{name: "value outside the const is a violation", method: "GET", path: "/healthz",
			reply: reply{200, js(), `{"status":"fine"}`}, wantViolation: true, wantOp: "getHealthz"},
		{name: "body that is not JSON is a violation", method: "GET", path: "/healthz",
			reply: reply{200, js(), `status=ok`}, wantViolation: true, wantOp: "getHealthz"},
		{name: "unlisted media type is a violation", method: "GET", path: "/healthz",
			reply: reply{200, map[string]string{"Content-Type": "text/html"}, `<p>ok</p>`}, wantViolation: true, wantOp: "getHealthz"},
		{name: "unlisted status code is a violation", method: "GET", path: "/healthz",
			reply: reply{418, js(), `{"error":{"code":"internal_error","message":"teapot","request_id":"r-1"}}`}, wantViolation: true, wantOp: "getHealthz"},
		{name: "listed 503 readiness passes", method: "GET", path: "/readyz",
			reply: reply{503, js(), `{"status":"not_ready","checks":{"database":"unavailable"}}`}, wantOp: "getReadyz"},
		{name: "readiness with an extra check is a violation", method: "GET", path: "/readyz",
			reply: reply{200, js(), `{"status":"ready","checks":{"database":"ok","cache":"ok"}}`}, wantViolation: true, wantOp: "getReadyz"},

		{name: "conforming campaign passes", method: "GET", path: "/api/campaigns/" + campID,
			reply: reply{200, js(), validCamp}, wantOp: "getCampaign"},
		{name: "campaign without a nullable key is a violation", method: "GET", path: "/api/campaigns/" + campID,
			reply: reply{200, js(), strings.Replace(validCamp, `"approval":null,`, "", 1)}, wantViolation: true, wantOp: "getCampaign"},
		{name: "campaign with an unknown state is a violation", method: "GET", path: "/api/campaigns/" + campID,
			reply: reply{200, js(), strings.Replace(validCamp, `"state":"draft"`, `"state":"failed"`, 1)}, wantViolation: true, wantOp: "getCampaign"},
		{name: "conforming 404 passes", method: "GET", path: "/api/campaigns/" + campID,
			reply: reply{404, js(), notFoundBody}, wantOp: "getCampaign"},
		{name: "404 with an error code the response does not allow is a violation", method: "GET", path: "/api/campaigns/" + campID,
			reply: reply{404, js(), `{"error":{"code":"invalid_state","message":"x","request_id":"r-1"}}`}, wantViolation: true, wantOp: "getCampaign"},
		{name: "404 carrying fields is a violation", method: "GET", path: "/api/campaigns/" + campID,
			reply: reply{404, js(), `{"error":{"code":"campaign_not_found","message":"x","fields":[{"path":"","code":"invalid_value","message":"x"}],"request_id":"r-1"}}`}, wantViolation: true, wantOp: "getCampaign"},
		{name: "error without request_id is a violation", method: "GET", path: "/api/campaigns/" + campID,
			reply: reply{404, js(), `{"error":{"code":"campaign_not_found","message":"x"}}`}, wantViolation: true, wantOp: "getCampaign"},
		{name: "422 without fields is a violation", method: "POST", path: "/api/campaigns", reqBody: `{"brief":""}`,
			reply: reply{422, js(), `{"error":{"code":"validation_failed","message":"x","request_id":"r-1"}}`}, wantViolation: true, wantOp: "createCampaign"},

		{name: "valid request answered 201 passes", method: "POST", path: "/api/campaigns", reqBody: `{"brief":"x"}`,
			reply: reply{201, js("Location", "/api/campaigns/"+campID), validCamp}, wantOp: "createCampaign"},
		{name: "invalid request answered 201 is a violation", method: "POST", path: "/api/campaigns", reqBody: `{"brief":"x","state":"approved"}`,
			reply: reply{201, js("Location", "/api/campaigns/"+campID), validCamp}, wantViolation: true, wantOp: "createCampaign"},
		{name: "invalid request answered 422 passes", method: "POST", path: "/api/campaigns", reqBody: `{"brief":"x","state":"approved"}`,
			reply: reply{422, js(), validationBody}, wantOp: "createCampaign"},
		{name: "malformed UUID path answered 404 maps to generateSegment", method: "POST", path: "/api/campaigns/not-a-uuid/segment",
			reply: reply{404, js(), notFoundBody}, wantOp: "generateSegment"},

		{name: "unknown path answered RouteNotFound passes", method: "GET", path: "/api/no-such-endpoint",
			reply: reply{404, js(), `{"error":{"code":"not_found","message":"not found","request_id":"r-1"}}`}},
		{name: "unknown path answered 200 is a violation", method: "GET", path: "/api/no-such-endpoint",
			reply: reply{200, js(), `{"status":"ok"}`}, wantViolation: true},
		{name: "unknown path with an operation error code is a violation", method: "GET", path: "/api/no-such-endpoint",
			reply: reply{404, js(), notFoundBody}, wantViolation: true},
		{name: "unsupported method answered MethodNotAllowed passes", method: "DELETE", path: "/api/campaigns",
			reply: reply{405, js("Allow", "GET, POST"), `{"error":{"code":"method_not_allowed","message":"method not allowed","request_id":"r-1"}}`}},
		{name: "405 without Allow is a violation", method: "DELETE", path: "/api/campaigns",
			reply: reply{405, js(), `{"error":{"code":"method_not_allowed","message":"method not allowed","request_id":"r-1"}}`}, wantViolation: true},
		{name: "unsupported method answered 404 is a violation", method: "DELETE", path: "/api/campaigns",
			reply: reply{404, js(), `{"error":{"code":"not_found","message":"x","request_id":"r-1"}}`}, wantViolation: true},

		{name: "JSON error of an SSE operation is still validated", method: "GET", path: "/api/campaigns/" + campID + "/events",
			reply: reply{404, js(), `{"error":{"code":"invalid_state","message":"x","request_id":"r-1"}}`}, wantViolation: true, wantOp: "streamCampaignEvents"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := serve(t, tc.reply)
			c, tr := strictClient(srv.URL)
			_, err := doVia(t, c, tc.method, srv.URL+tc.path, tc.reqBody)
			if tc.wantViolation {
				if !errors.Is(err, apispec.ErrViolation) {
					t.Fatalf("error = %v, want a violation (apispec.ErrViolation)", err)
				}
				if msg := err.Error(); !strings.Contains(msg, tc.path) || !strings.Contains(msg, "openapi.yaml") {
					t.Fatalf("violation message does not name the request and the spec: %s", msg)
				}
				if len(tr.Violations()) != 1 {
					t.Fatalf("Violations() = %d entries, want 1", len(tr.Violations()))
				}
			} else if err != nil {
				t.Fatalf("conforming exchange rejected: %v", err)
			}
			want := map[string]int{}
			if tc.wantOp != "" {
				want[tc.wantOp] = 1
			}
			if diff := cmp.Diff(want, tr.Calls()); diff != "" {
				t.Fatalf("recorded operationIds mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// REQ-TRK-01, REQ-CNT-04
// text/event-stream bodies of x-sse-events operations are excluded from body validation and are not
// buffered: the response arrives while the server still holds the stream open, the first event is
// readable, and the operationId is recorded.
func TestStrictTransport_DoesNotBufferOrValidateSSEBodies(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "retry: 3000\n\nevent: snapshot\ndata: {\"anything\":\"not schema-checked\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	c, tr := strictClient(srv.URL)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second) // safety bound only; never reached when the stream is not buffered
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/campaigns/"+campID+"/events", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("SSE response rejected or buffered: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || line != "retry: 3000\n" {
		t.Fatalf("first stream line = %q, %v; want %q", line, err, "retry: 3000\n")
	}
	if diff := cmp.Diff(map[string]int{"streamCampaignEvents": 1}, tr.Calls()); diff != "" {
		t.Fatalf("recorded operationIds mismatch (-want +got):\n%s", diff)
	}
}

// REQ-DLV-02
// Without OPENAPI_VALIDATE=strict, and for hosts outside the API scope (e.g. the mock provider),
// the transport passes responses through untouched and records nothing.
func TestStrictTransport_PassesThroughWhenOffOrOutOfScope(t *testing.T) {
	t.Parallel()
	bad := reply{200, js(), `{"status":"ok","extra":true}`}
	t.Run("strict off", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, bad)
		tr := &apispec.Transport{Strict: false, Scope: srv.URL}
		if _, err := doVia(t, &http.Client{Transport: tr}, "GET", srv.URL+"/healthz", ""); err != nil {
			t.Fatalf("non-strict transport rejected a response: %v", err)
		}
		if len(tr.Calls()) != 0 {
			t.Fatalf("non-strict transport recorded calls: %v", tr.Calls())
		}
	})
	t.Run("host outside the scope", func(t *testing.T) {
		t.Parallel()
		srv := serve(t, bad)
		tr := &apispec.Transport{Strict: true, Scope: "http://api.invalid:8080"}
		if _, err := doVia(t, &http.Client{Transport: tr}, "GET", srv.URL+"/healthz", ""); err != nil {
			t.Fatalf("out-of-scope request was validated: %v", err)
		}
		if len(tr.Calls()) != 0 {
			t.Fatalf("out-of-scope request recorded calls: %v", tr.Calls())
		}
	})
}

// REQ-AUD-00
// The coverage rule of the E2E TestMain: every operation outside the phase allowlist must have been
// called; allowlisted operations are exempt; the allowlist names only real operationIds.
func TestCoverage_UncoveredOperations(t *testing.T) {
	t.Parallel()
	spec := loadSpec(t)
	all := map[string]int{}
	for _, op := range spec.Operations() {
		all[op.ID] = 1
	}

	t.Run("everything called leaves nothing uncovered", func(t *testing.T) {
		t.Parallel()
		if got := spec.Uncovered(all, nil); len(got) != 0 {
			t.Fatalf("Uncovered = %v, want none", got)
		}
	})
	t.Run("a missing implemented operation is reported", func(t *testing.T) {
		t.Parallel()
		calls := map[string]int{}
		for k, v := range all {
			calls[k] = v
		}
		delete(calls, "listCampaigns")
		if diff := cmp.Diff([]string{"listCampaigns"}, spec.Uncovered(calls, contract.NotImplemented)); diff != "" {
			t.Fatalf("Uncovered mismatch (-want +got):\n%s", diff)
		}
	})
	t.Run("allowlisted operations are exempt", func(t *testing.T) {
		t.Parallel()
		calls := map[string]int{}
		for k, v := range all {
			if _, skip := contract.NotImplemented[k]; !skip {
				calls[k] = v
			}
		}
		if got := spec.Uncovered(calls, contract.NotImplemented); len(got) != 0 {
			t.Fatalf("Uncovered = %v, want none", got)
		}
	})
	t.Run("the P3 allowlist names only spec operations", func(t *testing.T) {
		t.Parallel()
		if got := spec.UnknownIDs(contract.NotImplemented); len(got) != 0 {
			t.Fatalf("UnknownIDs = %v, want none", got)
		}
		if got := spec.UnknownIDs(map[string]string{"deleteCampaign": "P9"}); !slices.Equal(got, []string{"deleteCampaign"}) {
			t.Fatalf("UnknownIDs = %v, want [deleteCampaign]", got)
		}
	})
}
