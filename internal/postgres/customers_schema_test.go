package postgres_test

import (
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"

	"github.com/gulcint/smart-campaign-composer/test/testdb"
)

// REQ-SEED-02
// customers has exactly the db-schema.md §3.1 columns: no national id, birth date, gender,
// address or payment columns.
func TestCustomersTable_HasNoSensitiveColumns(t *testing.T) {
	pool := testdb.App(t)
	rows, err := pool.Query(t.Context(),
		`SELECT column_name FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'customers'`)
	if err != nil {
		t.Fatalf("query columns: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("collect columns: %v", err)
	}
	want := []string{
		"city", "created_at", "email", "email_opt_in", "email_opt_in_at", "first_name", "id", "language",
		"last_name", "last_seen_at", "loyalty_tier", "phone", "sms_opt_in", "sms_opt_in_at",
	}
	slices.Sort(got)
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("customers columns mismatch (-want +got):\n%s", diff)
	}
}
