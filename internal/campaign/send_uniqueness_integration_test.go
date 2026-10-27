package campaign_test

// REQ-SND-03 at the database level, over the real Postgres of compose.test.yml (TEST_DATABASE_URL,
// scc_app). Uses the harness of service_integration_test.go; skips under -short (testdb.App).
// Creates its own campaign and never mutates customers; the duplicate insert runs in a transaction
// that is always rolled back.

import (
	"context"
	"testing"

	"github.com/gulcint/smart-campaign-composer/internal/llm"
	"github.com/gulcint/smart-campaign-composer/test/testdb"
)

// REQ-SND-03, REQ-SND-01
// After an accepted Send the campaign has exactly audience.sms + audience.email delivery rows, at
// most one per (channel, customer). Copying an existing delivery row of either channel under a fresh
// id is refused by the database with SQLSTATE 23505 on deliveries_campaign_channel_customer_key, and
// the row count is unchanged.
func TestDeliveries_UniquePerCampaignChannelCustomer(t *testing.T) {
	t.Parallel()
	h := newMockHarness(t)
	id := h.audienceReady(t)
	v := h.mustValidated(t, scen(llm.ScenarioOK), id)
	if _, err := h.svc.Approve(context.Background(), id, v.ContentVersionID); err != nil {
		t.Fatalf("Approve() error = %v", err)
	}
	if _, err := h.svc.Send(context.Background(), id, v.ContentVersionID); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	want := h.m.ExampleBrief.Audience.SMS + h.m.ExampleBrief.Audience.Email
	before := h.deliveryCount(t, id)
	if before != want {
		t.Fatalf("deliveries after Send = %d, want audience sms + email = %d", before, want)
	}
	var dups int
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM (
			SELECT 1 FROM deliveries WHERE campaign_id = $1 GROUP BY channel, customer_id HAVING count(*) > 1
		) AS d`, id).Scan(&dups); err != nil {
		t.Fatalf("count duplicate (channel, customer_id) pairs: %v", err)
	}
	if dups != 0 {
		t.Fatalf("%d (channel, customer_id) pairs have more than one delivery, want 0", dups)
	}

	for _, ch := range []string{"sms", "email"} {
		t.Run("a second "+ch+" delivery for the same customer is refused", func(t *testing.T) {
			var did string
			if err := h.pool.QueryRow(t.Context(), `SELECT id::text FROM deliveries
				WHERE campaign_id = $1 AND channel::text = $2 ORDER BY customer_id LIMIT 1`, id, ch).Scan(&did); err != nil {
				t.Fatalf("select one %s delivery: %v", ch, err)
			}
			tx, err := h.pool.Begin(t.Context())
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			_, err = tx.Exec(t.Context(), `INSERT INTO deliveries
				SELECT (jsonb_populate_record(d, jsonb_build_object('id', gen_random_uuid()))).*
				FROM deliveries AS d WHERE d.id::text = $1`, did)
			pgErr := testdb.PgError(err)
			if pgErr == nil {
				t.Fatalf("duplicate %s delivery insert error = %v, want SQLSTATE 23505", ch, err)
			}
			if pgErr.Code != "23505" || pgErr.ConstraintName != "deliveries_campaign_channel_customer_key" {
				t.Fatalf("duplicate %s delivery insert = SQLSTATE %s on %q, want 23505 on deliveries_campaign_channel_customer_key", ch, pgErr.Code, pgErr.ConstraintName)
			}
		})
	}

	if after := h.deliveryCount(t, id); after != before {
		t.Fatalf("deliveries after the refused inserts = %d, want %d", after, before)
	}
}
