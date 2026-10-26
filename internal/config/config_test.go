package config_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/gulcint/smart-campaign-composer/internal/config"
	"github.com/gulcint/smart-campaign-composer/internal/delivery"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/secret"
)

// fakeKey looks like an Anthropic key but is too short for the gitleaks rule; it is never valid.
const (
	fakeKey    = "sk-ant-test-0123"
	dbPassword = "s3cr3t-db-pw"
	dbURL      = "postgres://scc_app:" + dbPassword + "@db:5432/scc?sslmode=disable"
)

func getenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// REQ-AI-03, REQ-AI-05
// With an empty environment every documented default applies; LLM_PROVIDER defaults to auto and
// no key is set, so the application runs with the mock LLM (go-interfaces.md §config).
func TestLoad_Defaults(t *testing.T) {
	t.Parallel()
	got, err := config.Load(getenv(nil))
	if err != nil {
		t.Fatalf("Load(empty env) error = %v; DATABASE_URL is checked by the binaries, not by Load", err)
	}
	want := config.Config{
		HTTPAddr:              ":8080",
		WorkerHTTPAddr:        ":8081",
		LogLevel:              slog.LevelInfo,
		LLMMode:               llm.ModeAuto,
		AnthropicModel:        "claude-opus-5-5",
		LLMTimeout:            60 * time.Second,
		LLMMaxTokens:          4096,
		MockLLMScenario:       llm.ScenarioOK,
		MockLLMChunkDelay:     0,
		ProviderBaseURL:       "http://mockprovider:8090",
		ProviderTimeout:       10 * time.Second,
		DispatchRatePerSecond: 100,
		DispatchConcurrency:   32,
		DispatchBatchSize:     50,
		DeliveryLease:         60 * time.Second,
		Retry: delivery.RetryConfig{
			MaxAttempts:     5,
			BaseDelay:       time.Second,
			MaxDelay:        60 * time.Second,
			MaxRateLimited:  20,
			RateLimitWindow: 10 * time.Minute,
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("Load(empty env) mismatch (-want +got):\n%s", diff)
	}
	if got.AnthropicAPIKey.IsSet() {
		t.Fatal("AnthropicAPIKey.IsSet() = true with no ANTHROPIC_API_KEY")
	}
	if diff := cmp.Diff(delivery.DefaultRetryConfig(), got.Retry); diff != "" {
		t.Fatalf("Retry differs from delivery.DefaultRetryConfig() (-want +got):\n%s", diff)
	}
}

// REQ-AI-05
// The key is read from the ANTHROPIC_API_KEY environment variable (compose loads it from .env).
func TestLoad_ReadsAnthropicAPIKeyFromEnvironment(t *testing.T) {
	t.Parallel()
	got, err := config.Load(getenv(map[string]string{"ANTHROPIC_API_KEY": fakeKey}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !got.AnthropicAPIKey.IsSet() {
		t.Fatal("AnthropicAPIKey.IsSet() = false, want true")
	}
	if got.AnthropicAPIKey.Reveal() != fakeKey {
		t.Fatal("AnthropicAPIKey.Reveal() does not return the ANTHROPIC_API_KEY value")
	}
}

// REQ-AI-03
func TestLoad_LLMProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		want    llm.Mode
		wantErr error
	}{
		{name: "unset means auto", env: map[string]string{}, want: llm.ModeAuto},
		{name: "auto", env: map[string]string{"LLM_PROVIDER": "auto"}, want: llm.ModeAuto},
		{name: "mock", env: map[string]string{"LLM_PROVIDER": "mock"}, want: llm.ModeMock},
		{name: "real with key", env: map[string]string{"LLM_PROVIDER": "real", "ANTHROPIC_API_KEY": fakeKey}, want: llm.ModeReal},
		{name: "real without key is not rejected by Load (llm.Select decides)", env: map[string]string{"LLM_PROVIDER": "real"}, want: llm.ModeReal},
		{name: "invalid value is rejected", env: map[string]string{"LLM_PROVIDER": "fake"}, wantErr: config.ErrInvalidValue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := config.Load(getenv(tc.env))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Load() error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && got.LLMMode != tc.want {
				t.Fatalf("LLMMode = %q, want %q", got.LLMMode, tc.want)
			}
			if err != nil && strings.Contains(err.Error(), fakeKey) {
				t.Fatalf("Load error contains the API key: %v", err)
			}
		})
	}
}

// REQ-AI-02
// MOCK_LLM_SCENARIO selects the mock's default scenario (llm-tools.md §3.1).
func TestLoad_MockLLMScenario(t *testing.T) {
	t.Parallel()
	got, err := config.Load(getenv(map[string]string{"MOCK_LLM_SCENARIO": "invalid_json"}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.MockLLMScenario != llm.ScenarioInvalidJSON {
		t.Fatalf("MockLLMScenario = %q, want %q", got.MockLLMScenario, llm.ScenarioInvalidJSON)
	}
}

// REQ-AI-04
// Logging or printing the configuration never reveals the API key or the database password.
func TestConfig_RendersSecretsMasked(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(getenv(map[string]string{
		"ANTHROPIC_API_KEY": fakeKey,
		"DATABASE_URL":      dbURL,
		"LLM_PROVIDER":      "auto",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.DatabaseURL.Reveal() != dbURL {
		t.Fatal("DatabaseURL.Reveal() does not return the DATABASE_URL value")
	}

	renders := []struct {
		name string
		out  func(t *testing.T) string
	}{
		{name: "String", out: func(*testing.T) string { return cfg.String() }},
		{name: "%v", out: func(*testing.T) string { return fmt.Sprintf("%v", cfg) }},
		{name: "%+v", out: func(*testing.T) string { return fmt.Sprintf("%+v", cfg) }},
		{name: "%#v", out: func(*testing.T) string { return fmt.Sprintf("%#v", cfg) }},
		{name: "%s", out: func(*testing.T) string { return fmt.Sprintf("%s", cfg) }},
		{name: "pointer %+v", out: func(*testing.T) string { return fmt.Sprintf("%+v", &cfg) }},
		{name: "slog JSON handler", out: func(*testing.T) string {
			var buf bytes.Buffer
			slog.New(slog.NewJSONHandler(&buf, nil)).Info("config loaded", "config", cfg)
			return buf.String()
		}},
		{name: "slog text handler", out: func(*testing.T) string {
			var buf bytes.Buffer
			slog.New(slog.NewTextHandler(&buf, nil)).Info("config loaded", slog.Any("config", cfg))
			return buf.String()
		}},
		{name: "LogValue", out: func(*testing.T) string { return cfg.LogValue().Resolve().String() }},
		{name: "json.Marshal", out: func(t *testing.T) string {
			b, err := json.Marshal(cfg)
			if err != nil {
				t.Fatalf("json.Marshal(cfg) error = %v", err)
			}
			return string(b)
		}},
	}
	for _, tc := range renders {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out := tc.out(t)
			if strings.Contains(out, fakeKey) {
				t.Fatalf("output contains the API key: %s", out)
			}
			if strings.Contains(out, dbPassword) {
				t.Fatalf("output contains the database password: %s", out)
			}
		})
	}

	t.Run("String shows the mask for set secrets", func(t *testing.T) {
		t.Parallel()
		if !strings.Contains(cfg.String(), secret.Mask) {
			t.Fatalf("String() = %q, want it to contain %q", cfg.String(), secret.Mask)
		}
	})
}
