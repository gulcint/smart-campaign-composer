// Command api serves the HTTP API (docs/contracts/openapi.yaml).
//
// Configuration comes from the environment; when a .env file exists in the working directory
// its variables are used for keys that are not already set (compose passes them via env_file).
// The process exits with code 1 if the configuration is invalid, DATABASE_URL is missing or the
// LLM client cannot be built (e.g. LLM_PROVIDER=real without ANTHROPIC_API_KEY).
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/config"
	"github.com/gulcint/smart-campaign-composer/internal/httpapi"
	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/internal/metrics"
	"github.com/gulcint/smart-campaign-composer/internal/postgres"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("api stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	if err := loadDotEnv(".env"); err != nil {
		return err
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	logger.Info("api starting", slog.Any("config", cfg))

	if !cfg.DatabaseURL.IsSet() {
		return fmt.Errorf("%w: DATABASE_URL", config.ErrMissing)
	}

	clk := clock.Real()
	client, provider, err := llm.New(llm.FactoryConfig{
		Mode:      cfg.LLMMode,
		APIKey:    cfg.AnthropicAPIKey,
		Model:     cfg.AnthropicModel,
		Timeout:   cfg.LLMTimeout,
		MaxTokens: cfg.LLMMaxTokens,
		Mock: llm.MockOptions{
			DefaultScenario: cfg.MockLLMScenario,
			ChunkDelay:      cfg.MockLLMChunkDelay,
			Clock:           clk,
		},
		Logger: logger,
	})
	if err != nil {
		return err
	}
	_ = client // used by the campaign service from P3 on

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.Open(ctx, cfg.DatabaseURL.Reveal())
	if err != nil {
		return err
	}
	defer pool.Close()

	handler := httpapi.New(httpapi.Deps{
		LLMProvider: provider,
		DB:          pool,
		Metrics:     metrics.NewAPIHandler(postgres.NewMetricsSource(pool)),
		Clock:       clk,
		Logger:      logger,
	})
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("api listening", slog.String("addr", cfg.HTTPAddr), slog.String("llm_provider", string(provider)))
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	logger.Info("api shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// loadDotEnv sets KEY=VALUE pairs from path for keys that are not set yet. A missing file is
// not an error. Values are never logged.
func loadDotEnv(path string) error {
	f, err := os.Open(path) //nolint:gosec // fixed relative path
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, set := os.LookupEnv(key); !set && key != "" {
			if err := os.Setenv(key, value); err != nil {
				return fmt.Errorf("read %s: %w", path, err)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}
