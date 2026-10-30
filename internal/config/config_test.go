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

// REQ-AI-03, REQ-SND-02, REQ-SND-04, REQ-SND-05
// With an empty environment: LLM_PROVIDER defaults to auto with no key (=> mock LLM), the
// provider address defaults to the mock provider, the dispatch rate is 100/s per channel and the
// retry policy has the catalogue's constants (5 attempts incl. the first, 1s base, 60s cap,
// 20 x 429 or 10 minutes).
func TestLoad_Defaults(t *testing.T) {
	t.Parallel()
	got, err := config.Load(getenv(nil))
	if err != nil {
		t.Fatalf("Load(empty env) error = %v", err)
	}
	if got.LLMMode != llm.ModeAuto {
		t.Fatalf("LLMMode = %q, want %q", got.LLMMode, llm.ModeAuto)
	}
	if got.AnthropicAPIKey.IsSet() {
		t.Fatal("AnthropicAPIKey.IsSet() = true with no ANTHROPIC_API_KEY")
	}
	if got.ProviderBaseURL != "http://mockprovider:8090" {
		t.Fatalf("ProviderBaseURL = %q, want the mock provider", got.ProviderBaseURL)
	}
	if got.DispatchRatePerSecond != 100 {
		t.Fatalf("DispatchRatePerSecond = %v, want 100", got.DispatchRatePerSecond)
	}
	wantRetry := delivery.RetryConfig{
		MaxAttempts:     5,
		BaseDelay:       time.Second,
		MaxDelay:        60 * time.Second,
		MaxRateLimited:  20,
		RateLimitWindow: 10 * time.Minute,
	}
	if diff := cmp.Diff(wantRetry, got.Retry); diff != "" {
		t.Fatalf("Retry mismatch (-want +got):\n%s", diff)
	}
}

// REQ-SND-02, REQ-SND-04, REQ-SND-05
// The worker's settings come from their environment variables: PROVIDER_BASE_URL,
// DISPATCH_RATE_PER_SECOND and RETRY_MAX_ATTEMPTS / RETRY_BASE_DELAY / RETRY_MAX_DELAY /
// RETRY_MAX_RATE_LIMITED / RETRY_RATE_LIMIT_WINDOW override the defaults of TestLoad_Defaults.
func TestLoad_WorkerEnvironment(t *testing.T) {
	t.Parallel()
	got, err := config.Load(getenv(map[string]string{
		"PROVIDER_BASE_URL":        "http://127.0.0.1:18090",
		"DISPATCH_RATE_PER_SECOND": "40",
		"RETRY_MAX_ATTEMPTS":       "3",
		"RETRY_BASE_DELAY":         "250ms",
		"RETRY_MAX_DELAY":          "5s",
		"RETRY_MAX_RATE_LIMITED":   "7",
		"RETRY_RATE_LIMIT_WINDOW":  "2m",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.ProviderBaseURL != "http://127.0.0.1:18090" {
		t.Fatalf("ProviderBaseURL = %q, want PROVIDER_BASE_URL", got.ProviderBaseURL)
	}
	if got.DispatchRatePerSecond != 40 {
		t.Fatalf("DispatchRatePerSecond = %v, want 40", got.DispatchRatePerSecond)
	}
	want := delivery.RetryConfig{
		MaxAttempts:     3,
		BaseDelay:       250 * time.Millisecond,
		MaxDelay:        5 * time.Second,
		MaxRateLimited:  7,
		RateLimitWindow: 2 * time.Minute,
	}
	if diff := cmp.Diff(want, got.Retry); diff != "" {
		t.Fatalf("Retry mismatch (-want +got):\n%s", diff)
	}
}

// REQ-SCH-01 C9, REQ-SCH-02 C11
// SCHEDULE_MIN_LEAD (config.Config.ScheduleMinLead: default 1m, allowed 0s..24h) and
// SCHEDULER_INTERVAL (config.Config.SchedulerInterval: default 1s, allowed > 0 and <= 1m); a value
// outside the range or not a time.ParseDuration string is config.ErrInvalidValue.
func TestLoad_ScheduleSettings(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		env          map[string]string
		wantLead     time.Duration
		wantInterval time.Duration
		wantErr      error
	}{
		{name: "defaults", env: map[string]string{}, wantLead: time.Minute, wantInterval: time.Second},
		{name: "min lead 0s is allowed", env: map[string]string{"SCHEDULE_MIN_LEAD": "0s"}, wantLead: 0, wantInterval: time.Second},
		{name: "min lead 24h is allowed", env: map[string]string{"SCHEDULE_MIN_LEAD": "24h"}, wantLead: 24 * time.Hour, wantInterval: time.Second},
		{name: "min lead 90s", env: map[string]string{"SCHEDULE_MIN_LEAD": "90s"}, wantLead: 90 * time.Second, wantInterval: time.Second},
		{name: "min lead over 24h is invalid", env: map[string]string{"SCHEDULE_MIN_LEAD": "24h0m1s"}, wantErr: config.ErrInvalidValue},
		{name: "negative min lead is invalid", env: map[string]string{"SCHEDULE_MIN_LEAD": "-1s"}, wantErr: config.ErrInvalidValue},
		{name: "min lead that is no duration is invalid", env: map[string]string{"SCHEDULE_MIN_LEAD": "bir dakika"}, wantErr: config.ErrInvalidValue},
		{name: "interval 1m is allowed", env: map[string]string{"SCHEDULER_INTERVAL": "1m"}, wantLead: time.Minute, wantInterval: time.Minute},
		{name: "interval 1ms is allowed", env: map[string]string{"SCHEDULER_INTERVAL": "1ms"}, wantLead: time.Minute, wantInterval: time.Millisecond},
		{name: "interval 0s is invalid", env: map[string]string{"SCHEDULER_INTERVAL": "0s"}, wantErr: config.ErrInvalidValue},
		{name: "negative interval is invalid", env: map[string]string{"SCHEDULER_INTERVAL": "-1s"}, wantErr: config.ErrInvalidValue},
		{name: "interval over 1m is invalid", env: map[string]string{"SCHEDULER_INTERVAL": "1m0.001s"}, wantErr: config.ErrInvalidValue},
		{name: "interval that is no duration is invalid", env: map[string]string{"SCHEDULER_INTERVAL": "10"}, wantErr: config.ErrInvalidValue},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := config.Load(getenv(tc.env))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Load(%v) error = %v, want %v", tc.env, err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if got.ScheduleMinLead != tc.wantLead {
				t.Fatalf("ScheduleMinLead = %s, want %s", got.ScheduleMinLead, tc.wantLead)
			}
			if got.SchedulerInterval != tc.wantInterval {
				t.Fatalf("SchedulerInterval = %s, want %s", got.SchedulerInterval, tc.wantInterval)
			}
		})
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
// Logging or printing the configuration never reveals the API key.
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
		})
	}

	t.Run("String shows the mask for set secrets", func(t *testing.T) {
		t.Parallel()
		if !strings.Contains(cfg.String(), secret.Mask) {
			t.Fatalf("String() = %q, want it to contain %q", cfg.String(), secret.Mask)
		}
	})
}
