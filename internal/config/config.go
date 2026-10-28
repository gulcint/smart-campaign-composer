// Package config reads the runtime configuration from environment variables.
// Secrets are held as secret.Secret, so a logged Config never shows them (REQ-AI-04).
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/delivery"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/secret"
)

type Config struct {
	HTTPAddr       string        // HTTP_ADDR, default ":8080" (api)
	WorkerHTTPAddr string        // WORKER_HTTP_ADDR, default ":8081" (worker health/metrics)
	DatabaseURL    secret.Secret // DATABASE_URL, required for api/worker
	LogLevel       slog.Level    // LOG_LEVEL: debug|info|warn|error, default info

	AnthropicAPIKey   secret.Secret // ANTHROPIC_API_KEY, optional
	LLMMode           llm.Mode      // LLM_PROVIDER: auto|real|mock, default auto
	AnthropicModel    string        // ANTHROPIC_MODEL, default "claude-sonnet-5-5"
	LLMTimeout        time.Duration // LLM_TIMEOUT, default 60s
	LLMMaxTokens      int           // LLM_MAX_TOKENS, default 4096
	MockLLMScenario   llm.Scenario  // MOCK_LLM_SCENARIO, default "ok"
	MockLLMChunkDelay time.Duration // MOCK_LLM_CHUNK_DELAY, default 0s

	SegmentReferenceTime *time.Time // SEGMENT_REFERENCE_TIME (RFC 3339), optional; nil => Clock.Now()

	ProviderBaseURL       string               // PROVIDER_BASE_URL, default "http://mockprovider:8090"
	ProviderTimeout       time.Duration        // PROVIDER_TIMEOUT, default 10s
	DispatchRatePerSecond int                  // DISPATCH_RATE_PER_SECOND, default 100 (per channel)
	DispatchConcurrency   int                  // DISPATCH_CONCURRENCY, default 32
	DispatchBatchSize     int                  // DISPATCH_BATCH_SIZE, default 50
	DeliveryLease         time.Duration        // DELIVERY_LEASE, default 60s
	Retry                 delivery.RetryConfig // RETRY_MAX_ATTEMPTS(5), RETRY_BASE_DELAY(1s), RETRY_MAX_DELAY(60s),
	// RETRY_MAX_RATE_LIMITED(20), RETRY_RATE_LIMIT_WINDOW(10m)
	WorkerRandSeed int64 // WORKER_RAND_SEED, default 0 => seeded from crypto/rand
}

var (
	ErrInvalidValue = errors.New("config: invalid value") // wrapped with the variable name, e.g. "LLM_PROVIDER"
	ErrMissing      = errors.New("config: required variable missing")
)

// Load reads configuration through getenv (tests pass a map lookup; binaries pass os.Getenv).
// Empty string == unset. Durations use time.ParseDuration syntax. Every problem is reported,
// joined, each wrapping ErrInvalidValue and naming its variable.
func Load(getenv func(string) string) (Config, error) {
	l := loader{getenv: getenv}
	retry := delivery.DefaultRetryConfig()
	c := Config{
		HTTPAddr:       l.str("HTTP_ADDR", ":8080"),
		WorkerHTTPAddr: l.str("WORKER_HTTP_ADDR", ":8081"),
		DatabaseURL:    secret.Secret(getenv("DATABASE_URL")),
		LogLevel:       l.level("LOG_LEVEL"),

		AnthropicAPIKey:   secret.Secret(getenv("ANTHROPIC_API_KEY")),
		LLMMode:           l.mode("LLM_PROVIDER"),
		AnthropicModel:    l.str("ANTHROPIC_MODEL", "claude-sonnet-5-5"),
		LLMTimeout:        l.duration("LLM_TIMEOUT", 60*time.Second, false),
		LLMMaxTokens:      l.integer("LLM_MAX_TOKENS", 4096, 1),
		MockLLMScenario:   l.scenario("MOCK_LLM_SCENARIO"),
		MockLLMChunkDelay: l.duration("MOCK_LLM_CHUNK_DELAY", 0, true),

		SegmentReferenceTime: l.timestamp("SEGMENT_REFERENCE_TIME"),

		ProviderBaseURL:       l.str("PROVIDER_BASE_URL", "http://mockprovider:8090"),
		ProviderTimeout:       l.duration("PROVIDER_TIMEOUT", 10*time.Second, false),
		DispatchRatePerSecond: l.integer("DISPATCH_RATE_PER_SECOND", 100, 1),
		DispatchConcurrency:   l.integer("DISPATCH_CONCURRENCY", 32, 1),
		DispatchBatchSize:     l.integer("DISPATCH_BATCH_SIZE", 50, 1),
		DeliveryLease:         l.duration("DELIVERY_LEASE", 60*time.Second, false),
		Retry: delivery.RetryConfig{
			// Upper bounds = the deliveries CHECKs (failed_attempts <= 5, rate_limited_count <= 20).
			MaxAttempts:     l.boundedInteger("RETRY_MAX_ATTEMPTS", retry.MaxAttempts, 1, retry.MaxAttempts),
			BaseDelay:       l.duration("RETRY_BASE_DELAY", retry.BaseDelay, false),
			MaxDelay:        l.duration("RETRY_MAX_DELAY", retry.MaxDelay, false),
			MaxRateLimited:  l.boundedInteger("RETRY_MAX_RATE_LIMITED", retry.MaxRateLimited, 1, retry.MaxRateLimited),
			RateLimitWindow: l.duration("RETRY_RATE_LIMIT_WINDOW", retry.RateLimitWindow, false),
		},
		WorkerRandSeed: l.int64("WORKER_RAND_SEED", 0),
	}
	if err := errors.Join(l.errs...); err != nil {
		return Config{}, err
	}
	return c, nil
}

type loader struct {
	getenv func(string) string
	errs   []error
}

func (l *loader) invalid(name string, cause error) {
	if cause != nil {
		l.errs = append(l.errs, fmt.Errorf("%w: %s: %w", ErrInvalidValue, name, cause))
		return
	}
	l.errs = append(l.errs, fmt.Errorf("%w: %s", ErrInvalidValue, name))
}

func (l *loader) str(name, def string) string {
	if v := l.getenv(name); v != "" {
		return v
	}
	return def
}

func (l *loader) integer(name string, def, minValue int) int {
	v := l.getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < minValue {
		l.invalid(name, nil)
		return def
	}
	return n
}

// boundedInteger is integer with an inclusive upper bound.
func (l *loader) boundedInteger(name string, def, minValue, maxValue int) int {
	n := l.integer(name, def, minValue)
	if n > maxValue {
		l.invalid(name, nil)
		return def
	}
	return n
}

func (l *loader) int64(name string, def int64) int64 {
	v := l.getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		l.invalid(name, nil)
		return def
	}
	return n
}

func (l *loader) duration(name string, def time.Duration, allowZero bool) time.Duration {
	v := l.getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil || d < 0 || (d == 0 && !allowZero) {
		l.invalid(name, nil)
		return def
	}
	return d
}

func (l *loader) timestamp(name string) *time.Time {
	v := l.getenv(name)
	if v == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		l.invalid(name, nil)
		return nil
	}
	t = t.UTC()
	return &t
}

func (l *loader) level(name string) slog.Level {
	switch v := strings.ToLower(strings.TrimSpace(l.getenv(name))); v {
	case "":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		l.invalid(name, nil)
		return slog.LevelInfo
	}
}

func (l *loader) mode(name string) llm.Mode {
	m, err := llm.ParseMode(l.getenv(name))
	if err != nil {
		l.invalid(name, err)
		return llm.ModeAuto
	}
	return m
}

func (l *loader) scenario(name string) llm.Scenario {
	v := l.getenv(name)
	if v == "" {
		return llm.ScenarioOK
	}
	s, err := llm.ParseScenario(v)
	if err != nil {
		l.invalid(name, err)
		return llm.ScenarioOK
	}
	return s
}

// plain has Config's fields but none of its methods, so formatting it cannot recurse into
// String; secret.Secret fields still format through their own masking methods.
type plain Config

// String renders the configuration with secrets masked.
func (c Config) String() string {
	return fmt.Sprintf("%+v", plain(c))
}

// LogValue renders the configuration for slog with secrets masked.
func (c Config) LogValue() slog.Value {
	ref := ""
	if c.SegmentReferenceTime != nil {
		ref = c.SegmentReferenceTime.Format(time.RFC3339)
	}
	return slog.GroupValue(
		slog.String("http_addr", c.HTTPAddr),
		slog.String("worker_http_addr", c.WorkerHTTPAddr),
		slog.Any("database_url", c.DatabaseURL),
		slog.String("log_level", c.LogLevel.String()),
		slog.Any("anthropic_api_key", c.AnthropicAPIKey),
		slog.String("llm_provider", string(c.LLMMode)),
		slog.String("anthropic_model", c.AnthropicModel),
		slog.Duration("llm_timeout", c.LLMTimeout),
		slog.Int("llm_max_tokens", c.LLMMaxTokens),
		slog.String("mock_llm_scenario", string(c.MockLLMScenario)),
		slog.Duration("mock_llm_chunk_delay", c.MockLLMChunkDelay),
		slog.String("segment_reference_time", ref),
		slog.String("provider_base_url", c.ProviderBaseURL),
		slog.Duration("provider_timeout", c.ProviderTimeout),
		slog.Int("dispatch_rate_per_second", c.DispatchRatePerSecond),
		slog.Int("dispatch_concurrency", c.DispatchConcurrency),
		slog.Int("dispatch_batch_size", c.DispatchBatchSize),
		slog.Duration("delivery_lease", c.DeliveryLease),
		slog.Int("retry_max_attempts", c.Retry.MaxAttempts),
		slog.Duration("retry_base_delay", c.Retry.BaseDelay),
		slog.Duration("retry_max_delay", c.Retry.MaxDelay),
		slog.Int("retry_max_rate_limited", c.Retry.MaxRateLimited),
		slog.Duration("retry_rate_limit_window", c.Retry.RateLimitWindow),
		slog.Int64("worker_rand_seed", c.WorkerRandSeed),
	)
}
