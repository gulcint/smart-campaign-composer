package mockprovider_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/mockprovider"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// REQ-MOCK-01, REQ-MOCK-02
// With an empty environment LoadConfig returns the documented defaults.
func TestLoadConfig_Defaults(t *testing.T) {
	t.Parallel()
	got, err := mockprovider.LoadConfig(env(nil))
	if err != nil {
		t.Fatalf("LoadConfig(empty env) error = %v", err)
	}
	checks := []struct {
		name string
		ok   bool
		got  any
		want any
	}{
		{"Addr", got.Addr == ":8090", got.Addr, ":8090"},
		{"Seed", got.Seed == 1, got.Seed, 1},
		{"ErrorRate", got.ErrorRate == 0.05, got.ErrorRate, 0.05},
		{"RateLimitRate", got.RateLimitRate == 0.02, got.RateLimitRate, 0.02},
		{"LostResponseRate", got.LostResponseRate == 0, got.LostResponseRate, 0},
		{"LatencyMin", got.LatencyMin == 20*time.Millisecond, got.LatencyMin, 20 * time.Millisecond},
		{"LatencyMax", got.LatencyMax == 200*time.Millisecond, got.LatencyMax, 200 * time.Millisecond},
		{"RateLimitPerSecond", got.RateLimitPerSecond == 100, got.RateLimitPerSecond, 100},
		{"RetryAfterSeconds", got.RetryAfterSeconds == 1, got.RetryAfterSeconds, 1},
		{"AdminEnabled", !got.AdminEnabled, got.AdminEnabled, false},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// REQ-MOCK-01, REQ-MOCK-02
// Every MOCK_PROVIDER_* variable overrides its default.
func TestLoadConfig_Overrides(t *testing.T) {
	t.Parallel()
	got, err := mockprovider.LoadConfig(env(map[string]string{
		"MOCK_PROVIDER_ADDR":                  ":18090",
		"MOCK_PROVIDER_SEED":                  "42",
		"MOCK_PROVIDER_ERROR_RATE":            "0.3",
		"MOCK_PROVIDER_RATE_LIMIT_RATE":       "0.2",
		"MOCK_PROVIDER_LOST_RESPONSE_RATE":    "0.1",
		"MOCK_PROVIDER_LATENCY_MIN":           "0s",
		"MOCK_PROVIDER_LATENCY_MAX":           "5ms",
		"MOCK_PROVIDER_RATE_LIMIT_PER_SECOND": "50",
		"MOCK_PROVIDER_RETRY_AFTER_SECONDS":   "3",
		"MOCK_PROVIDER_ADMIN":                 "true",
	}))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	checks := []struct {
		name string
		ok   bool
		got  any
		want any
	}{
		{"Addr", got.Addr == ":18090", got.Addr, ":18090"},
		{"Seed", got.Seed == 42, got.Seed, 42},
		{"ErrorRate", got.ErrorRate == 0.3, got.ErrorRate, 0.3},
		{"RateLimitRate", got.RateLimitRate == 0.2, got.RateLimitRate, 0.2},
		{"LostResponseRate", got.LostResponseRate == 0.1, got.LostResponseRate, 0.1},
		{"LatencyMin", got.LatencyMin == 0, got.LatencyMin, 0},
		{"LatencyMax", got.LatencyMax == 5*time.Millisecond, got.LatencyMax, 5 * time.Millisecond},
		{"RateLimitPerSecond", got.RateLimitPerSecond == 50, got.RateLimitPerSecond, 50},
		{"RetryAfterSeconds", got.RetryAfterSeconds == 3, got.RetryAfterSeconds, 3},
		{"AdminEnabled", got.AdminEnabled, got.AdminEnabled, true},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// REQ-MOCK-01
// Rates lie in [0, 1] and together may not exceed 1; any invalid value is
// mockprovider.ErrInvalidConfig (the process then exits with code 1).
func TestLoadConfig_Invalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     map[string]string
		wantErr error
	}{
		{name: "all rates 0 is valid", env: map[string]string{"MOCK_PROVIDER_ERROR_RATE": "0", "MOCK_PROVIDER_RATE_LIMIT_RATE": "0", "MOCK_PROVIDER_LOST_RESPONSE_RATE": "0"}},
		{name: "error rate 1 alone is valid", env: map[string]string{"MOCK_PROVIDER_ERROR_RATE": "1", "MOCK_PROVIDER_RATE_LIMIT_RATE": "0"}},
		{name: "rates summing to exactly 1 are valid", env: map[string]string{"MOCK_PROVIDER_ERROR_RATE": "0.5", "MOCK_PROVIDER_RATE_LIMIT_RATE": "0.25", "MOCK_PROVIDER_LOST_RESPONSE_RATE": "0.25"}},
		{name: "error rate above 1", env: map[string]string{"MOCK_PROVIDER_ERROR_RATE": "1.5"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "negative error rate", env: map[string]string{"MOCK_PROVIDER_ERROR_RATE": "-0.1"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "rate limit rate above 1", env: map[string]string{"MOCK_PROVIDER_RATE_LIMIT_RATE": "2"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "negative lost response rate", env: map[string]string{"MOCK_PROVIDER_LOST_RESPONSE_RATE": "-1"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "rates summing above 1", env: map[string]string{"MOCK_PROVIDER_ERROR_RATE": "0.6", "MOCK_PROVIDER_RATE_LIMIT_RATE": "0.5"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "three rates summing above 1", env: map[string]string{"MOCK_PROVIDER_ERROR_RATE": "0.4", "MOCK_PROVIDER_RATE_LIMIT_RATE": "0.4", "MOCK_PROVIDER_LOST_RESPONSE_RATE": "0.3"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "error rate not a number", env: map[string]string{"MOCK_PROVIDER_ERROR_RATE": "often"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "seed not a number", env: map[string]string{"MOCK_PROVIDER_SEED": "abc"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "latency min not a duration", env: map[string]string{"MOCK_PROVIDER_LATENCY_MIN": "fast"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "latency max not a duration", env: map[string]string{"MOCK_PROVIDER_LATENCY_MAX": "slow"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "rate limit per second not a number", env: map[string]string{"MOCK_PROVIDER_RATE_LIMIT_PER_SECOND": "many"}, wantErr: mockprovider.ErrInvalidConfig},
		{name: "retry after seconds not a whole number", env: map[string]string{"MOCK_PROVIDER_RETRY_AFTER_SECONDS": "soon"}, wantErr: mockprovider.ErrInvalidConfig},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := mockprovider.LoadConfig(env(tc.env))
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("LoadConfig(%v) error = %v, want %v", tc.env, err, tc.wantErr)
			}
		})
	}
}
