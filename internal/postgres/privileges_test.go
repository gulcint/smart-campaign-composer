package postgres_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/test/testdb"
)

// Every probe runs inside a transaction that is always rolled back, with a short lock_timeout, so a
// wrongly granted privilege can neither damage the shared test database nor block other tests.

// REQ-SEC-02
// The application role (scc_app) cannot change the schema: DDL and TRUNCATE fail with SQLSTATE
// 42501 insufficient_privilege (db-schema.md §1).
func TestAppRole_CannotChangeSchema(t *testing.T) {
	pool := testdb.App(t)
	tests := []struct {
		name string
		sql  string
	}{
		{name: "DROP TABLE customers is denied", sql: `DROP TABLE customers`},
		{name: "CREATE TABLE is denied", sql: `CREATE TABLE scc_privilege_probe (id int)`},
		{name: "ALTER TABLE campaigns ADD COLUMN is denied", sql: `ALTER TABLE campaigns ADD COLUMN scc_privilege_probe int`},
		{name: "TRUNCATE customers is denied", sql: `TRUNCATE customers`},
		{name: "DROP TABLE deliveries is denied", sql: `DROP TABLE deliveries`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := inRolledBackTx(t, pool, func(tx pgx.Tx) error {
				_, err := tx.Exec(t.Context(), tc.sql)
				return err
			})
			pgErr := testdb.PgError(err)
			if pgErr == nil {
				t.Fatalf("%s as scc_app: error = %v, want SQLSTATE 42501", tc.sql, err)
			}
			if pgErr.Code != "42501" {
				t.Fatalf("%s as scc_app: SQLSTATE = %s (%s), want 42501 insufficient_privilege", tc.sql, pgErr.Code, pgErr.Message)
			}
		})
	}
}

// REQ-SEC-02
// scc_app is connected as itself, owns no table, has no CREATE on public, and can still use the data.
func TestAppRole_IsLeastPrivileged(t *testing.T) {
	pool := testdb.App(t)
	ctx := t.Context()

	var user string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&user); err != nil {
		t.Fatalf("current_user: %v", err)
	}
	if user != "scc_app" {
		t.Fatalf("TEST_DATABASE_URL connects as %q, want scc_app", user)
	}

	var canCreate bool
	if err := pool.QueryRow(ctx, `SELECT has_schema_privilege('public', 'CREATE')`).Scan(&canCreate); err != nil {
		t.Fatalf("has_schema_privilege: %v", err)
	}
	if canCreate {
		t.Fatal("scc_app has CREATE on schema public, want none")
	}

	var owned int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tableowner = current_user`).Scan(&owned); err != nil {
		t.Fatalf("pg_tables: %v", err)
	}
	if owned != 0 {
		t.Fatalf("scc_app owns %d tables in public, want 0", owned)
	}

	var owner string
	if err := pool.QueryRow(ctx, `SELECT tableowner FROM pg_tables WHERE schemaname = 'public' AND tablename = 'customers'`).Scan(&owner); err != nil {
		t.Fatalf("owner of customers: %v", err)
	}
	if owner != "scc_migrator" {
		t.Fatalf("customers is owned by %q, want scc_migrator", owner)
	}

	// DML still works for the application.
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM customers`).Scan(&n); err != nil {
		t.Fatalf("SELECT as scc_app failed: %v", err)
	}
}

// REQ-SEC-02
// The denial is role-based: the migrator role (scc_migrator) can run DDL.
func TestMigratorRole_CanChangeSchema(t *testing.T) {
	pool := testdb.Migrator(t)
	err := inRolledBackTx(t, pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(t.Context(), `CREATE TABLE scc_privilege_probe (id int)`)
		return err
	})
	if err != nil {
		t.Fatalf("CREATE TABLE as scc_migrator error = %v, want nil", err)
	}
}

// inRolledBackTx runs fn in a transaction that is always rolled back.
func inRolledBackTx(t *testing.T, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	t.Helper()
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '2s'`); err != nil {
		t.Fatalf("set lock_timeout: %v", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '10s'`); err != nil {
		t.Fatalf("set statement_timeout: %v", err)
	}
	return fn(tx)
}
