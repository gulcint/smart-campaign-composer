// Command seed generates the deterministic fictional dataset, writes its manifest (and
// optionally a JSONL export) and loads it into the database (docs/contracts/seed.md §1).
//
// Exit codes: 0 success · 1 runtime error (DB, dataset mismatch) · 2 usage error.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/clock"
	"github.com/gulcint/smart-campaign-composer/internal/postgres"
	"github.com/gulcint/smart-campaign-composer/internal/secret"
	"github.com/gulcint/smart-campaign-composer/internal/seed"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	fs.SetOutput(stderr)
	seedValue := fs.Int64("seed", 42, "randomness seed")
	users := fs.Int("users", 5000, "number of customers (at least 200)")
	refTime := fs.String("ref-time", "now", "reference time: RFC 3339 or now (rounded to the second, UTC)")
	manifestPath := fs.String("manifest", "testdata/seed/manifest.json", "manifest output path (- = stdout)")
	exportPath := fs.String("export", "", "optional JSONL export path")
	dbURL := fs.String("db-url", "", "target database (default $DATABASE_URL)")
	skipDB := fs.Bool("skip-db", false, "do not connect to the database; only write manifest/export")
	reset := fs.Bool("reset", false, "delete a different dataset before loading")
	ifEmpty := fs.Bool("if-empty", false, "do nothing (exit 0) if the database already holds a seed run")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "seed: unexpected arguments: %v\n", fs.Args())
		return 2
	}
	if *users < seed.MinUsers || *users > seed.MaxUsers {
		_, _ = fmt.Fprintf(stderr, "seed: --users must be between %d and %d\n", seed.MinUsers, seed.MaxUsers)
		return 2
	}
	if *ifEmpty && *reset {
		_, _ = fmt.Fprintln(stderr, "seed: --if-empty and --reset are mutually exclusive")
		return 2
	}
	ref, err := parseRefTime(*refTime)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
		return 2
	}
	url := secret.Secret(*dbURL)
	if !url.IsSet() {
		url = secret.Secret(os.Getenv("DATABASE_URL"))
	}
	if !*skipDB && !url.IsSet() {
		_, _ = fmt.Fprintln(stderr, "seed: --db-url or DATABASE_URL is required unless --skip-db is set")
		return 2
	}

	ds, err := seed.Generate(seed.Options{Seed: *seedValue, Users: *users, RefTime: ref})
	if err != nil {
		if errors.Is(err, seed.ErrTooFewUsers) || errors.Is(err, seed.ErrInvalidRefTime) {
			_, _ = fmt.Fprintf(stderr, "seed: %v\n", err)
			return 2
		}
		logger.Error("generate failed", slog.String("error", err.Error()))
		return 1
	}
	m := ds.Manifest()
	manifest, err := m.MarshalCanonical()
	if err != nil {
		logger.Error("manifest encoding failed", slog.String("error", err.Error()))
		return 1
	}
	if err := writeOutput(*manifestPath, manifest, stdout); err != nil {
		logger.Error("write manifest failed", slog.String("error", err.Error()))
		return 1
	}
	if *exportPath != "" {
		f, err := os.Create(*exportPath) //nolint:gosec // operator-chosen output path
		if err != nil {
			logger.Error("write export failed", slog.String("error", err.Error()))
			return 1
		}
		werr := ds.WriteJSONL(f)
		cerr := f.Close()
		if err := errors.Join(werr, cerr); err != nil {
			logger.Error("write export failed", slog.String("error", err.Error()))
			return 1
		}
	}
	if *skipDB {
		logger.Info("seed generated", slog.Int("customers", m.Totals.Customers), slog.Int("events", m.Totals.Events),
			slog.String("dataset_sha256", m.DatasetSHA256))
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.Open(ctx, url.Reveal())
	if err != nil {
		logger.Error("database connection failed", slog.String("error", err.Error()))
		return 1
	}
	defer pool.Close()
	if *ifEmpty {
		seeded, err := seed.HasRun(ctx, pool)
		if err != nil {
			logger.Error("seed state check failed", slog.String("error", err.Error()))
			return 1
		}
		if seeded {
			logger.Info("seed skipped: the database already holds a seed run (--if-empty)")
			return 0
		}
	}
	if err := seed.Load(ctx, pool, ds, *reset); err != nil {
		logger.Error("seed load failed", slog.String("error", err.Error()))
		return 1
	}
	logger.Info("seed loaded", slog.Int("customers", m.Totals.Customers), slog.Int("events", m.Totals.Events),
		slog.String("dataset_sha256", m.DatasetSHA256), slog.String("ref_time", m.Params.RefTime))
	return 0
}

func parseRefTime(v string) (time.Time, error) {
	if v == "now" {
		return clock.Real().Now().Truncate(time.Second), nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("--ref-time must be RFC 3339 or now: %q", v)
	}
	return t.UTC().Truncate(time.Second), nil
}

func writeOutput(path string, data []byte, stdout io.Writer) error {
	if path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	return os.WriteFile(path, data, 0o644) //nolint:gosec // the manifest is a public test fixture
}
