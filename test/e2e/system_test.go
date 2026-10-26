// Package e2e_test drives the running compose.test.yml stack over HTTP (docs/contracts/README.md §7).
// It belongs to the integration layer: it skips under -short and needs E2E_API_URL /
// E2E_MOCKPROVIDER_URL otherwise.
package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/test/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

var client = &http.Client{Timeout: 10 * time.Second}

func baseURL(t *testing.T, name string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e: needs the compose.test.yml stack (skipped under -short)")
	}
	u := os.Getenv(name)
	if u == "" {
		t.Fatalf("%s is not set; e2e tests run through `make test` (docs/contracts/README.md §7)", name)
	}
	return strings.TrimRight(u, "/")
}

func get(t *testing.T, url string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest(%s): %v", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body of %s: %v", url, err)
	}
	return resp.StatusCode, resp.Header, body
}

// REQ-TRK-03, REQ-DLV-01
// The api process is up (/healthz) and reaches its database (/readyz).
func TestE2E_APIHealthAndReadiness(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	tests := []struct {
		path       string
		wantStatus int
		wantBody   map[string]any
	}{
		{path: "/healthz", wantStatus: http.StatusOK, wantBody: map[string]any{"status": "ok"}},
		{path: "/readyz", wantStatus: http.StatusOK, wantBody: map[string]any{"status": "ready", "checks": map[string]any{"database": "ok"}}},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			status, hdr, body := get(t, api+tc.path)
			if status != tc.wantStatus {
				t.Fatalf("GET %s status = %d, want %d; body: %s", tc.path, status, tc.wantStatus, body)
			}
			if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("GET %s Content-Type = %q, want application/json", tc.path, ct)
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("GET %s body is not JSON (%v): %s", tc.path, err, body)
			}
			if diff := cmp.Diff(tc.wantBody, got); diff != "" {
				t.Fatalf("GET %s body mismatch (-want +got):\n%s", tc.path, diff)
			}
		})
	}
}

// REQ-AI-03, REQ-DLV-01, REQ-DLV-02
// Without a key the running api uses the mock LLM and says so on GET /api/config.
func TestE2E_ConfigReportsMockProvider(t *testing.T) {
	t.Parallel()
	api := baseURL(t, "E2E_API_URL")
	status, _, body := get(t, api+"/api/config")
	if status != http.StatusOK {
		t.Fatalf("GET /api/config status = %d, want 200; body: %s", status, body)
	}
	var cfg struct {
		LLMProvider   string   `json:"llm_provider"`
		MockScenarios []string `json:"mock_scenarios"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("body is not JSON (%v): %s", err, body)
	}
	if cfg.LLMProvider != "mock" {
		t.Fatalf("llm_provider = %q, want %q (key-less stack)", cfg.LLMProvider, "mock")
	}
	if len(cfg.MockScenarios) != 14 {
		t.Fatalf("mock_scenarios has %d entries, want 14", len(cfg.MockScenarios))
	}
	if strings.Contains(string(body), "sk-ant") {
		t.Fatalf("GET /api/config leaks something that looks like an API key: %s", body)
	}
}

// REQ-DLV-01
// The mock provider is part of the stack and answers its health check (metrics.md §3).
func TestE2E_MockProviderIsUp(t *testing.T) {
	t.Parallel()
	mp := baseURL(t, "E2E_MOCKPROVIDER_URL")
	status, _, body := get(t, mp+"/healthz")
	if status != http.StatusOK {
		t.Fatalf("GET mockprovider /healthz status = %d, want 200; body: %s", status, body)
	}
}
