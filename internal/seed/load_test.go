package seed_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/internal/seed"
	"github.com/gulcint/smart-campaign-composer/test/testdb"
)

// These integration tests share the compose.test.yml database with the E2E api, which is seeded
// with the canonical dataset (README §7). They therefore only (re)load the canonical dataset and
// never use reset=true; they are not parallel.

// REQ-SEED-01
// Loading the same dataset again does not duplicate records (seed.md §2).
func TestLoad_IsIdempotent(t *testing.T) {
	pool := testdb.App(t)
	ctx := t.Context()
	d := canonicalDataset(t)
	g := canonical(t)

	for run := 1; run <= 2; run++ {
		if err := seed.Load(ctx, pool, d, false); err != nil {
			t.Fatalf("Load run %d error = %v", run, err)
		}
		assertLoaded(t, ctx, pool, g)
	}
}

// REQ-SEED-01
// A different dataset is refused without --reset and leaves the database unchanged.
func TestLoad_DifferentDatasetWithoutResetIsRefused(t *testing.T) {
	pool := testdb.App(t)
	ctx := t.Context()
	g := canonical(t)
	if err := seed.Load(ctx, pool, canonicalDataset(t), false); err != nil {
		t.Fatalf("Load(canonical) error = %v", err)
	}

	other, err := seed.Generate(seed.Options{Seed: 43, Users: seed.MinUsers, RefTime: canonicalRefTime})
	if err != nil {
		t.Fatalf("Generate(seed 43) error = %v", err)
	}
	if err := seed.Load(ctx, pool, other, false); !errors.Is(err, seed.ErrDatasetMismatch) {
		t.Fatalf("Load(different dataset, reset=false) error = %v, want %v", err, seed.ErrDatasetMismatch)
	}
	assertLoaded(t, ctx, pool, g)
}

func canonicalDataset(t *testing.T) *seed.Dataset {
	t.Helper()
	d, err := seed.Generate(seed.Options{Seed: canonicalSeed, Users: canonicalUsers, RefTime: canonicalRefTime})
	if err != nil {
		t.Fatalf("Generate(canonical) error = %v", err)
	}
	return d
}

// assertLoaded checks customers = --users, events = totals.events and the single seed_runs row.
func assertLoaded(t *testing.T, ctx context.Context, pool *pgxpool.Pool, g *generated) {
	t.Helper()
	var customers, events, runs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&customers); err != nil {
		t.Fatalf("count customers: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM seed_runs`).Scan(&runs); err != nil {
		t.Fatalf("count seed_runs: %v", err)
	}
	if customers != g.doc.Totals.Customers {
		t.Errorf("customers = %d, want %d", customers, g.doc.Totals.Customers)
	}
	if events != g.doc.Totals.Events {
		t.Errorf("events = %d, want %d", events, g.doc.Totals.Events)
	}
	if runs != 1 {
		t.Fatalf("seed_runs rows = %d, want 1", runs)
	}

	var (
		seedVal int64
		users   int
		refTime time.Time
		sha     string
	)
	err := pool.QueryRow(ctx, `SELECT seed, users, ref_time, dataset_sha256 FROM seed_runs WHERE id = 1`).
		Scan(&seedVal, &users, &refTime, &sha)
	if err != nil {
		t.Fatalf("read seed_runs: %v", err)
	}
	if seedVal != canonicalSeed || users != canonicalUsers || !refTime.Equal(canonicalRefTime) || sha != g.doc.DatasetSHA256 {
		t.Errorf("seed_runs = (%d, %d, %s, %s), want (%d, %d, %s, %s)",
			seedVal, users, refTime.UTC().Format(time.RFC3339), sha,
			canonicalSeed, canonicalUsers, canonicalRefTime.Format(time.RFC3339), g.doc.DatasetSHA256)
	}
}
