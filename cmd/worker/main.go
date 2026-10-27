// Command worker sends queued deliveries to the provider: one dispatcher per channel, and only the
// holder of the channel's advisory lock sends (db-schema.md §4). It serves /healthz, /readyz and
// /metrics on WORKER_HTTP_ADDR.
//
// On SIGINT/SIGTERM the dispatchers stop claiming, requests already started are finished and
// recorded, the locks are released and the HTTP server shuts down. The process exits with code 1
// if the configuration is invalid or DATABASE_URL is missing.
package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/config"
	"github.com/gulcint/smart-campaign-composer/internal/delivery"
	"github.com/gulcint/smart-campaign-composer/internal/health"
	"github.com/gulcint/smart-campaign-composer/internal/metrics"
	"github.com/gulcint/smart-campaign-composer/internal/postgres"
	"github.com/gulcint/smart-campaign-composer/internal/provider"
)

const shutdownTimeout = 10 * time.Second

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)
	logger.Info("worker starting", slog.Any("config", cfg))

	if !cfg.DatabaseURL.IsSet() {
		return fmt.Errorf("%w: DATABASE_URL", config.ErrMissing)
	}
	seed, err := randSeed(cfg.WorkerRandSeed)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.Open(ctx, cfg.DatabaseURL.Reveal())
	if err != nil {
		return err
	}
	defer pool.Close()

	clk := clock.Real()
	store := postgres.NewDeliveryStore(pool)
	client := provider.NewHTTPClient(cfg.ProviderBaseURL, &http.Client{Timeout: cfg.ProviderTimeout}, clk)
	metricsHandler, recorder := metrics.NewWorkerHandler()
	// Rounded up: a truncated interval (e.g. 1s/3) would allow more than the rate per second.
	rate := time.Duration(cfg.DispatchRatePerSecond)
	interval := (time.Second + rate - 1) / rate
	if err := checkLease(cfg); err != nil {
		return err
	}

	var dispatchers []*delivery.Dispatcher
	for i, ch := range []delivery.Channel{delivery.ChannelSMS, delivery.ChannelEmail} {
		// Each channel has its own jitter stream (salt 1 = sms, 2 = email), so one channel's
		// draws never shift the other's delays.
		policy := delivery.NewRetryPolicy(cfg.Retry, rand.New(rand.NewPCG(seed, uint64(i+1)))) //nolint:gosec // jitter, not security
		d, err := delivery.NewDispatcher(delivery.DispatcherConfig{
			Channel:     ch,
			BatchSize:   cfg.DispatchBatchSize,
			Concurrency: cfg.DispatchConcurrency,
			Lease:       cfg.DeliveryLease,
		}, delivery.DispatcherDeps{
			Store:    store,
			Provider: client,
			Limiter:  delivery.NewTokenBucket(clk, interval),
			Policy:   policy,
			Clock:    clk,
			Logger:   logger,
			Recorder: recorder,
		})
		if err != nil {
			return err
		}
		dispatchers = append(dispatchers, d)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", health.Healthz())
	mux.Handle("GET /readyz", health.Readyz(pool))
	mux.Handle("GET /metrics", metricsHandler)
	srv := &http.Server{Addr: cfg.WorkerHTTPAddr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 1)
	go func() {
		logger.Info("worker listening", slog.String("addr", cfg.WorkerHTTPAddr), slog.String("provider_base_url", cfg.ProviderBaseURL))
		errc <- srv.ListenAndServe()
	}()

	var wg sync.WaitGroup
	for _, d := range dispatchers {
		wg.Go(func() {
			if err := d.Run(ctx); !errors.Is(err, context.Canceled) {
				logger.Error("dispatcher stopped", slog.String("error", err.Error()))
			}
		})
	}

	var serveErr error
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
		stop()
	case <-ctx.Done():
	}
	logger.Info("worker shutting down")
	wg.Wait()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && serveErr == nil {
		serveErr = fmt.Errorf("shutdown: %w", err)
	}
	return serveErr
}

// checkLease rejects a DELIVERY_LEASE that a claimed batch could outlive: the last job of a
// batch starts up to BatchSize permits later and its request may take PROVIDER_TIMEOUT; a lease
// shorter than that would let another claim resend a delivery whose request is still in flight
// (the provider dedups it, but the fenced store write of the old claim would then be dropped).
func checkLease(cfg config.Config) error {
	interval := time.Second / time.Duration(cfg.DispatchRatePerSecond)
	need := time.Duration(cfg.DispatchBatchSize)*interval + cfg.ProviderTimeout
	if cfg.DeliveryLease <= need {
		return fmt.Errorf("%w: DELIVERY_LEASE must exceed DISPATCH_BATCH_SIZE/DISPATCH_RATE_PER_SECOND + PROVIDER_TIMEOUT (%s)",
			config.ErrInvalidValue, need)
	}
	return nil
}

// randSeed returns WORKER_RAND_SEED, or a random seed when it is 0.
func randSeed(configured int64) (uint64, error) {
	if configured != 0 {
		return uint64(configured), nil //nolint:gosec // bit pattern of the seed
	}
	var b [8]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("worker: random seed: %w", err)
	}
	return binary.LittleEndian.Uint64(b[:]), nil
}
