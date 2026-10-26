// Package migrate applies and reverts the embedded SQL migrations with the
// privileged scc_migrator role (MIGRATE_DATABASE_URL). The application role
// scc_app never runs DDL (REQ-SEC-02).
package migrate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx5:// driver
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/gulcint/smart-campaign-composer/internal/secret"
	"github.com/gulcint/smart-campaign-composer/migrations"
)

// Up applies all pending migrations.
func Up(ctx context.Context, migratorURL secret.Secret) error {
	return run(ctx, migratorURL, (*migrate.Migrate).Up)
}

// Down reverts all migrations.
func Down(ctx context.Context, migratorURL secret.Secret) error {
	return run(ctx, migratorURL, (*migrate.Migrate).Down)
}

func run(ctx context.Context, migratorURL secret.Secret, step func(*migrate.Migrate) error) error {
	if !migratorURL.IsSet() {
		return errors.New("migrate: database url is empty")
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("migrate: open embedded migrations: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, driverURL(migratorURL.Reveal()))
	if err != nil {
		// The driver's error text can contain the URL; do not wrap it.
		return errors.New("migrate: cannot connect to the database")
	}
	defer func() { _, _ = m.Close() }()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			m.GracefulStop <- true
		case <-done:
		}
	}()

	if err := step(m); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("migrate: %w", ctxErr)
		}
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// driverURL maps postgres:// and postgresql:// URLs to golang-migrate's pgx5:// scheme.
func driverURL(u string) string {
	for _, p := range []string{"postgresql://", "postgres://"} {
		if strings.HasPrefix(u, p) {
			return "pgx5://" + strings.TrimPrefix(u, p)
		}
	}
	return u
}
