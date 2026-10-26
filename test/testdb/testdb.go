// Package testdb opens the Postgres pools used by integration tests (docs/contracts/README.md §7).
//
// Integration tests skip themselves under -short (make test-unit); otherwise the connection
// variables are mandatory and a missing one fails the test.
package testdb

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/internal/postgres"
)

// App returns a pool connected as scc_app (TEST_DATABASE_URL).
func App(t testing.TB) *pgxpool.Pool {
	t.Helper()
	return open(t, "TEST_DATABASE_URL")
}

// Migrator returns a pool connected as scc_migrator (TEST_MIGRATE_DATABASE_URL).
func Migrator(t testing.TB) *pgxpool.Pool {
	t.Helper()
	return open(t, "TEST_MIGRATE_DATABASE_URL")
}

func open(t testing.TB, envName string) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration: needs Postgres from compose.test.yml (skipped under -short)")
	}
	dsn := os.Getenv(envName)
	if dsn == "" {
		t.Fatalf("%s is not set; integration tests run through `make test` (docs/contracts/README.md §7)", envName)
	}
	pool, err := postgres.Open(context.Background(), dsn)
	if err != nil {
		// The DSN holds a password: never print it.
		t.Fatalf("postgres.Open(%s) failed: %v", envName, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// PgError returns the *pgconn.PgError inside err, or nil.
func PgError(err error) *pgconn.PgError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr
	}
	return nil
}
