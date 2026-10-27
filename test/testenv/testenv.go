// Package testenv is the shared test harness named in docs/contracts/go-interfaces.md §0.
//
// Every test package that builds an LLM client calls Main from its TestMain:
//
//	func TestMain(m *testing.M) { testenv.Main(m) }
//
// Main does two things before any test runs:
//
//  1. It calls llm.New once with the configuration read from the environment. If that
//     returns llm.ErrRealClientForbidden (a real Claude client was requested inside a test
//     binary), it prints a message to stderr and exits with code 1 (REQ-DLV-02).
//  2. It replaces http.DefaultTransport with a guard that refuses every request to a host
//     that is neither loopback nor one of the compose.test.yml services named by
//     E2E_API_URL / E2E_MOCKPROVIDER_URL, so tests cannot reach the Anthropic API or any other
//     real host (REQ-AI-02, docs/PLAN.md §4.5 "guard transport").
package testenv

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gulcint/smart-campaign-composer/internal/config"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
)

// ErrOutboundBlocked is returned (wrapped in *url.Error by http.Client) for a request to a host
// the guard does not allow.
var ErrOutboundBlocked = errors.New("testenv: outbound network request blocked in tests")

// allowedHostEnv lists the variables (docs/contracts/README.md §7) whose URL hosts tests may reach.
var allowedHostEnv = []string{"E2E_API_URL", "E2E_MOCKPROVIDER_URL"}

// Main installs the network guard, refuses a real LLM client and then runs the tests.
func Main(m *testing.M) { MainThen(m, nil) }

// MainThen is Main with a hook that runs after the tests and returns the final exit code (it may
// turn a passing run into a failing one, e.g. the E2E operationId coverage check of
// docs/contracts/README.md §11). A nil hook keeps m.Run's code.
func MainThen(m *testing.M, after func(code int) int) {
	installNetworkGuard(os.Getenv)
	if err := checkLLM(os.Getenv); err != nil {
		fmt.Fprintf(os.Stderr, "testenv: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	if after != nil {
		code = after(code)
	}
	os.Exit(code)
}

// checkLLM builds the LLM client exactly as the configuration in the environment asks for.
// The returned error never contains the API key: config and llm mask it, and only the mode is
// added here.
func checkLLM(getenv func(string) string) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	_, _, err = llm.New(llm.FactoryConfig{
		Mode:      cfg.LLMMode,
		APIKey:    cfg.AnthropicAPIKey,
		Model:     cfg.AnthropicModel,
		Timeout:   cfg.LLMTimeout,
		MaxTokens: cfg.LLMMaxTokens,
		Mock:      llm.MockOptions{DefaultScenario: cfg.MockLLMScenario},
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if errors.Is(err, llm.ErrRealClientForbidden) {
		return fmt.Errorf("a real LLM client was requested (LLM_PROVIDER=%s); tests must run with the mock LLM: %w", cfg.LLMMode, err)
	}
	if err != nil {
		return fmt.Errorf("construct LLM client: %w", err)
	}
	return nil
}

type guardTransport struct {
	next    http.RoundTripper
	allowed map[string]bool
}

func (g guardTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	host := strings.ToLower(r.URL.Hostname())
	if isLoopback(host) || g.allowed[host] {
		return g.next.RoundTrip(r)
	}
	if r.Body != nil {
		_ = r.Body.Close()
	}
	return nil, fmt.Errorf("%w: %s", ErrOutboundBlocked, host)
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func installNetworkGuard(getenv func(string) string) {
	allowed := map[string]bool{}
	for _, name := range allowedHostEnv {
		u, err := url.Parse(getenv(name))
		if err == nil && u.Hostname() != "" {
			allowed[strings.ToLower(u.Hostname())] = true
		}
	}
	http.DefaultTransport = guardTransport{next: http.DefaultTransport, allowed: allowed}
}
