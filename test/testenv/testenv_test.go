package testenv_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/gulcint/smart-campaign-composer/test/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }

// fakeKey looks like an Anthropic key but is too short for the gitleaks rule; it is never valid.
const fakeKey = "sk-ant-test-0123"

// REQ-DLV-02, REQ-AI-04
// The real LLM client cannot be selected in a test binary: testenv.Main ends the process with
// exit code 1 (go-interfaces.md §0, llm.New error precedence 2 and 3). The key never reaches the output.
func TestMain_ExitsWhenRealLLMClientIsRequested(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		env      []string
		wantCode int
	}{
		{name: "real without key exits with code 1", env: []string{"LLM_PROVIDER=real"}, wantCode: 1},
		{name: "real with key exits with code 1", env: []string{"LLM_PROVIDER=real", "ANTHROPIC_API_KEY=" + fakeKey}, wantCode: 1},
		{name: "auto with key exits with code 1", env: []string{"LLM_PROVIDER=auto", "ANTHROPIC_API_KEY=" + fakeKey}, wantCode: 1},
		{name: "unset provider with key exits with code 1", env: []string{"ANTHROPIC_API_KEY=" + fakeKey}, wantCode: 1},
		{name: "auto without key runs with the mock", env: []string{"LLM_PROVIDER=auto"}, wantCode: 0},
		{name: "unset provider without key runs with the mock", env: []string{}, wantCode: 0},
		{name: "mock with key runs with the mock", env: []string{"LLM_PROVIDER=mock", "ANTHROPIC_API_KEY=" + fakeKey}, wantCode: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Re-run this test binary with no tests selected: only TestMain (testenv.Main) runs.
			cmd := exec.Command(os.Args[0], "-test.run=^$")
			cmd.Env = append([]string{}, tc.env...) // non-nil: the child does NOT inherit our environment
			out, err := cmd.CombinedOutput()

			gotCode := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatalf("running the test binary: %v", err)
				}
				gotCode = exitErr.ExitCode()
			}
			if gotCode != tc.wantCode {
				t.Fatalf("exit code = %d, want %d; output:\n%s", gotCode, tc.wantCode, out)
			}
			if strings.Contains(string(out), fakeKey) {
				t.Fatalf("output contains the API key:\n%s", out)
			}
		})
	}
}

// REQ-AI-02
// Tests cannot reach the Anthropic API or any other real host; loopback (httptest) still works.
func TestMain_BlocksOutboundNetwork(t *testing.T) {
	t.Parallel()
	blocked := []string{
		"https://api.anthropic.com/v1/messages",
		"http://example.com/",
		"http://203.0.113.10:8080/healthz",
	}
	for _, target := range blocked {
		t.Run(target+" is blocked", func(t *testing.T) {
			t.Parallel()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader("{}"))
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			resp, err := http.DefaultClient.Do(req)
			if resp != nil {
				_ = resp.Body.Close()
			}
			if !errors.Is(err, testenv.ErrOutboundBlocked) {
				t.Fatalf("Do(%s) error = %v, want %v", target, err, testenv.ErrOutboundBlocked)
			}
		})
	}

	t.Run("loopback httptest server is reachable", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(srv.Close)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do(loopback) error = %v, want nil", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusNoContent)
		}
	})
}

// REQ-DLV-02, REQ-AI-02
// `make test` runs the suite in the compose.test.yml container without ANTHROPIC_API_KEY and
// with the mock LLM (docs/contracts/README.md §7). This check belongs to the integration layer:
// it skips under -short (make test-unit on the host) and runs in `make test`.
func TestMakeTestEnvironment_IsKeyLessWithMockLLM(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: checks the compose.test.yml container environment (skipped under -short)")
	}
	if v, ok := os.LookupEnv("ANTHROPIC_API_KEY"); ok && v != "" {
		t.Errorf("ANTHROPIC_API_KEY is set in the test environment; tests must run without a key")
	}
	if got := os.Getenv("LLM_PROVIDER"); got != "mock" {
		t.Errorf("LLM_PROVIDER = %q, want %q", got, "mock")
	}
}
