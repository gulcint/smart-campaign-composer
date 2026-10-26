// Command migrate applies (up, default) or reverts (down) the embedded migrations with the
// privileged role given in MIGRATE_DATABASE_URL. It runs once and exits (compose `migrate`).
//
// Usage: migrate [up|down]
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gulcint/smart-campaign-composer/internal/migrate"
	"github.com/gulcint/smart-campaign-composer/internal/secret"
)

func main() { os.Exit(run()) }

func run() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	direction := "up"
	if len(os.Args) > 1 {
		direction = os.Args[1]
	}
	if len(os.Args) > 2 || (direction != "up" && direction != "down") {
		_, _ = fmt.Fprintln(os.Stderr, "usage: migrate [up|down]")
		return 2
	}
	url := secret.Secret(os.Getenv("MIGRATE_DATABASE_URL"))
	if !url.IsSet() {
		logger.Error("MIGRATE_DATABASE_URL is required")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	step := migrate.Up
	if direction == "down" {
		step = migrate.Down
	}
	if err := step(ctx, url); err != nil {
		logger.Error("migration failed", slog.String("direction", direction), slog.String("error", err.Error()))
		return 1
	}
	logger.Info("migrations applied", slog.String("direction", direction), slog.Any("database_url", url))
	return 0
}
