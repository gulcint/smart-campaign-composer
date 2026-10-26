// Command mockprovider runs the mock SMS/email provider (docs/contracts/mock-provider.md).
// An invalid MOCK_PROVIDER_* value exits with code 1.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/mockprovider"
)

func main() { os.Exit(run()) }

func run() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := mockprovider.LoadConfig(os.Getenv)
	if err != nil {
		logger.Error("invalid configuration", slog.String("error", err.Error()))
		return 1
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mockprovider.New(cfg, clock.Real()),
		ReadHeaderTimeout: 10 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		logger.Info("mockprovider listening", slog.String("addr", cfg.Addr), slog.Int64("seed", cfg.Seed),
			slog.Bool("admin", cfg.AdminEnabled))
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("mockprovider stopped", slog.String("error", err.Error()))
			return 1
		}
		return 0
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return 1
	}
	return 0
}
