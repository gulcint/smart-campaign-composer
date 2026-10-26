package seed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const loadBatch = 2000

// Load upserts the dataset in one transaction. If seed_runs holds a different dataset_sha256 and
// reset is false => ErrDatasetMismatch. reset deletes deliveries, approvals, content_versions,
// campaigns, events, customers (in that order) before inserting. Only DML is used, so the
// least-privileged scc_app role is enough.
func Load(ctx context.Context, db *pgxpool.Pool, d *Dataset, reset bool) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("seed: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	// Serialize concurrent seed runs on the single seed_runs row.
	var stored string
	err = tx.QueryRow(ctx, `SELECT dataset_sha256 FROM seed_runs WHERE id = 1 FOR UPDATE`).Scan(&stored)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		stored = ""
	case err != nil:
		return fmt.Errorf("seed: read seed_runs: %w", err)
	}
	if stored != "" && stored != d.sha256Hex() {
		if !reset {
			return ErrDatasetMismatch
		}
		for _, table := range []string{"deliveries", "approvals", "content_versions", "campaigns", "events", "customers"} {
			// Table names come from the fixed list above, never from input.
			if _, err := tx.Exec(ctx, "DELETE FROM "+table); err != nil { //nolint:gosec // constant table names
				return fmt.Errorf("seed: reset %s: %w", table, err)
			}
		}
	}

	if err := upsertCustomers(ctx, tx, d.customers); err != nil {
		return err
	}
	if err := upsertEvents(ctx, tx, d.customers); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO seed_runs (id, seed, users, ref_time, dataset_sha256, loaded_at)
		VALUES (1, $1, $2, $3, $4, now())
		ON CONFLICT (id) DO UPDATE SET
			seed = EXCLUDED.seed, users = EXCLUDED.users, ref_time = EXCLUDED.ref_time,
			dataset_sha256 = EXCLUDED.dataset_sha256, loaded_at = EXCLUDED.loaded_at`,
		d.opts.Seed, d.opts.Users, d.opts.RefTime, d.sha256Hex()); err != nil {
		return fmt.Errorf("seed: write seed_runs: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("seed: commit: %w", err)
	}
	return nil
}

// HasRun reports whether seed_runs already records a loaded dataset (any parameters).
func HasRun(ctx context.Context, db *pgxpool.Pool) (bool, error) {
	var exists bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM seed_runs WHERE id = 1)`).Scan(&exists); err != nil {
		return false, fmt.Errorf("seed: read seed_runs: %w", err)
	}
	return exists, nil
}

func upsertCustomers(ctx context.Context, tx pgx.Tx, customers []customer) error {
	for start := 0; start < len(customers); start += loadBatch {
		batch := customers[start:min(start+loadBatch, len(customers))]
		n := len(batch)
		var (
			ids, firsts, lasts, cities, langs, tiers = make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n), make([]string, n)
			emails, phones                           = make([]*string, n), make([]*string, n)
			smsIn, emailIn                           = make([]bool, n), make([]bool, n)
			smsAt, emailAt, seenAt                   = make([]*time.Time, n), make([]*time.Time, n), make([]*time.Time, n)
			created                                  = make([]time.Time, n)
		)
		for i, c := range batch {
			ids[i], firsts[i], lasts[i], cities[i], langs[i], tiers[i] = c.id, c.firstName, c.lastName, c.city, c.language, c.loyaltyTier
			emails[i], phones[i] = c.email, c.phone
			smsIn[i], emailIn[i] = c.smsOptIn, c.emailOptIn
			smsAt[i], emailAt[i], seenAt[i] = c.smsOptInAt, c.emailOptInAt, c.lastSeenAt
			created[i] = c.createdAt
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO customers (id, first_name, last_name, email, phone, city, language, loyalty_tier,
				sms_opt_in, sms_opt_in_at, email_opt_in, email_opt_in_at, created_at, last_seen_at)
			SELECT * FROM unnest($1::uuid[], $2::text[], $3::text[], $4::text[], $5::text[], $6::text[],
				$7::text[], $8::text[], $9::boolean[], $10::timestamptz[], $11::boolean[], $12::timestamptz[],
				$13::timestamptz[], $14::timestamptz[])
			ON CONFLICT (id) DO UPDATE SET
				first_name = EXCLUDED.first_name, last_name = EXCLUDED.last_name, email = EXCLUDED.email,
				phone = EXCLUDED.phone, city = EXCLUDED.city, language = EXCLUDED.language,
				loyalty_tier = EXCLUDED.loyalty_tier, sms_opt_in = EXCLUDED.sms_opt_in,
				sms_opt_in_at = EXCLUDED.sms_opt_in_at, email_opt_in = EXCLUDED.email_opt_in,
				email_opt_in_at = EXCLUDED.email_opt_in_at, created_at = EXCLUDED.created_at,
				last_seen_at = EXCLUDED.last_seen_at`,
			ids, firsts, lasts, emails, phones, cities, langs, tiers, smsIn, smsAt, emailIn, emailAt, created, seenAt)
		if err != nil {
			return fmt.Errorf("seed: upsert customers: %w", err)
		}
	}
	return nil
}

func upsertEvents(ctx context.Context, tx pgx.Tx, customers []customer) error {
	var all []*event
	for i := range customers {
		for j := range customers[i].events {
			all = append(all, &customers[i].events[j])
		}
	}
	for start := 0; start < len(all); start += loadBatch {
		batch := all[start:min(start+loadBatch, len(all))]
		n := len(batch)
		ids, custIDs, types := make([]string, n), make([]string, n), make([]string, n)
		at := make([]time.Time, n)
		amounts, cats := make([]*string, n), make([]*string, n)
		for i, e := range batch {
			ids[i], custIDs[i], types[i], at[i], cats[i] = e.id, e.customerID, e.typ, e.occurredAt, e.category
			if e.typ == "purchase" {
				amounts[i] = ptr(amountString(e.amountCents))
			}
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO events (id, customer_id, type, occurred_at, amount, product_category)
			SELECT * FROM unnest($1::uuid[], $2::uuid[], $3::text[], $4::timestamptz[], $5::text[]::numeric[], $6::text[])
			ON CONFLICT (id) DO UPDATE SET
				customer_id = EXCLUDED.customer_id, type = EXCLUDED.type, occurred_at = EXCLUDED.occurred_at,
				amount = EXCLUDED.amount, product_category = EXCLUDED.product_category`,
			ids, custIDs, types, at, amounts, cats)
		if err != nil {
			return fmt.Errorf("seed: upsert events: %w", err)
		}
	}
	return nil
}
