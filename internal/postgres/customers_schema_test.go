package postgres_test

import (
	"slices"
	"testing"
	"time"

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

// REQ-SEED-02
// The database itself refuses real-looking contact data: customers_email_fictional_check and
// customers_phone_fictional_check (SQLSTATE 23514).
func TestCustomersTable_RejectsNonFictionalContactData(t *testing.T) {
	pool := testdb.App(t)
	createdAt := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	str := func(s string) *string { return &s }
	tests := []struct {
		name           string
		email, phone   *string
		wantConstraint string // "" => accepted
	}{
		{name: "example.com email and fictional phone are accepted", email: str("ayse.yilmaz.9001@example.com"), phone: str("+905000001234")},
		{name: "example.test email is accepted", email: str("ayse.yilmaz.9002@example.test"), phone: str("+905000001235")},
		{name: "missing email and phone are accepted", email: nil, phone: nil},
		{name: "real email domain is rejected", email: str("ayse.yilmaz@gmail.com"), phone: str("+905000001236"), wantConstraint: "customers_email_fictional_check"},
		{name: "example.org email is rejected", email: str("ayse@example.org"), phone: nil, wantConstraint: "customers_email_fictional_check"},
		{name: "uppercase email is rejected", email: str("Ayse@example.com"), phone: nil, wantConstraint: "customers_email_fictional_check"},
		{name: "real mobile number is rejected", email: nil, phone: str("+905321234567"), wantConstraint: "customers_phone_fictional_check"},
		{name: "phone with spaces is rejected", email: nil, phone: str("+90 500 000 12 34"), wantConstraint: "customers_phone_fictional_check"},
		{name: "phone with too many digits is rejected", email: nil, phone: str("+9050000012345"), wantConstraint: "customers_phone_fictional_check"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := inRolledBackTx(t, pool, func(tx pgx.Tx) error {
				_, err := tx.Exec(t.Context(),
					`INSERT INTO customers (id, first_name, last_name, email, phone, city, language, loyalty_tier, sms_opt_in, email_opt_in, created_at)
					 VALUES ('6f1c0c3e-0000-4000-8000-000000000001', 'Ayşe', 'Yılmaz', $1, $2, 'İstanbul', 'tr', 'bronze', false, false, $3)`,
					tc.email, tc.phone, createdAt)
				return err
			})
			if tc.wantConstraint == "" {
				if err != nil {
					t.Fatalf("insert error = %v, want nil", err)
				}
				return
			}
			pgErr := testdb.PgError(err)
			if pgErr == nil || pgErr.Code != "23514" || pgErr.ConstraintName != tc.wantConstraint {
				t.Fatalf("insert error = %v, want SQLSTATE 23514 on %s", err, tc.wantConstraint)
			}
		})
	}
}
