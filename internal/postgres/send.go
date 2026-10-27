package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gulcint/smart-campaign-composer/internal/campaign"
	"github.com/gulcint/smart-campaign-composer/internal/segment"
)

// Send acceptance and the API's read side of the delivery queue (db-schema.md §3.7, §3.9, §4).
// The worker's side of the queue (claim, lease, Mark*) is delivery.Store.

// CreateDeliveries accepts a send under the campaign row lock (campaign.Store).
func (s *CampaignStore) CreateDeliveries(ctx context.Context, id, versionID string, at time.Time, check func(campaign.LockedCampaign) error) (campaign.DeliveryTotals, campaign.State, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return campaign.DeliveryTotals{}, "", fmt.Errorf("postgres: send: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	lc, err := lockCampaign(ctx, tx, id)
	if err != nil {
		return campaign.DeliveryTotals{}, "", err
	}
	if err := check(lc.LockedCampaign); err != nil {
		return campaign.DeliveryTotals{}, "", err
	}
	if !lc.HasSegment {
		return campaign.DeliveryTotals{}, "", fmt.Errorf("%w: campaign has no segment", campaign.ErrInvalidState)
	}
	r, err := segment.Parse(append(append([]byte(`{"rule":`), lc.rule...), '}'))
	if err != nil {
		return campaign.DeliveryTotals{}, "", fmt.Errorf("postgres: stored segment rule: %w", err)
	}
	comp, err := segment.Compile(r, lc.refTime.UTC())
	if err != nil {
		return campaign.DeliveryTotals{}, "", fmt.Errorf("postgres: stored segment rule: %w", err)
	}
	// One row per (channel, customer) of the channel audiences (segment-dsl.md §6), read with the
	// current consent and contact columns. The compiled predicate holds only constant SQL and $n
	// placeholders; this query's own parameters follow the rule's. The row id is the provider
	// Idempotency-Key (REQ-SND-03).
	n := len(comp.Args)
	p := func(i int) string { return "$" + strconv.Itoa(n+i) }
	q := `WITH ins AS (
		INSERT INTO deliveries (id, campaign_id, content_version_id, customer_id, channel, status,
			next_attempt_at, created_at, updated_at)
		SELECT gen_random_uuid(), ` + p(1) + `::uuid, ` + p(2) + `::uuid, c.id, ch.channel, 'queued',
			` + p(3) + `::timestamptz, ` + p(3) + `::timestamptz, ` + p(3) + `::timestamptz
		FROM customers c
		JOIN customer_stats_at($1) s ON s.customer_id = c.id
		CROSS JOIN (VALUES ('sms'), ('email')) AS ch (channel)
		WHERE (` + comp.Where + `) AND CASE ch.channel
			WHEN 'sms' THEN c.sms_opt_in AND c.phone IS NOT NULL
			ELSE c.email_opt_in AND c.email IS NOT NULL END
		RETURNING channel)
	SELECT count(*) FILTER (WHERE channel = 'sms'), count(*) FILTER (WHERE channel = 'email') FROM ins`
	var t campaign.DeliveryTotals
	if err := tx.QueryRow(ctx, q, append(comp.Args, id, versionID, at)...).Scan(&t.SMS, &t.Email); err != nil {
		return campaign.DeliveryTotals{}, "", fmt.Errorf("postgres: create deliveries: %w", err)
	}
	t.Total = t.SMS + t.Email

	if _, err := tx.Exec(ctx, `
		UPDATE campaigns SET state = 'sending', sending_started_at = $2, updated_at = $2 WHERE id = $1`,
		id, at); err != nil {
		return campaign.DeliveryTotals{}, "", fmt.Errorf("postgres: start sending: %w", err)
	}
	state := campaign.StateSending
	// Both audiences empty: the campaign is complete in the same transaction (approved -> sending
	// -> completed; the state trigger has no direct approved -> completed).
	done, err := completeCampaignIfDone(ctx, tx, id, at)
	if err != nil {
		return campaign.DeliveryTotals{}, "", err
	}
	if done {
		state = campaign.StateCompleted
	}
	if err := tx.Commit(ctx); err != nil {
		return campaign.DeliveryTotals{}, "", fmt.Errorf("postgres: send: %w", err)
	}
	return t, state, nil
}

// completeCampaignIfDone moves a sending campaign to completed when none of its deliveries is
// queued or sending (db-schema.md §4 "Tamamlama"). It runs inside the caller's transaction: the
// send acceptance calls it (no delivery at all), and every delivery.Store write that moves a
// delivery to sent or failed must call it in the same transaction. It reports whether the
// campaign was completed by this call.
func completeCampaignIfDone(ctx context.Context, tx pgx.Tx, campaignID string, now time.Time) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE campaigns SET state = 'completed', completed_at = $2, updated_at = $2
		WHERE id = $1 AND state = 'sending' AND NOT EXISTS (
			SELECT 1 FROM deliveries WHERE campaign_id = $1 AND status IN ('queued', 'sending'))`,
		campaignID, now)
	if err != nil {
		return false, fmt.Errorf("postgres: complete campaign: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// DeliveryStatus reads the campaign's delivery counters (openapi CampaignStatus).
func (s *CampaignStore) DeliveryStatus(ctx context.Context, id string) (campaign.CampaignStatus, error) {
	var (
		st                   campaign.CampaignStatus
		state                string
		sendingAt, completed *time.Time
	)
	err := s.db.QueryRow(ctx, `
		SELECT state, sending_started_at, completed_at,
			greatest(updated_at, (SELECT max(updated_at) FROM deliveries WHERE campaign_id = $1))
		FROM campaigns WHERE id = $1`, id).Scan(&state, &sendingAt, &completed, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignStatus{}, campaign.ErrCampaignNotFound
	}
	if err != nil {
		return campaign.CampaignStatus{}, fmt.Errorf("postgres: campaign status: %w", err)
	}
	st.CampaignID, st.State = id, campaign.State(state)
	st.SendingStartedAt, st.CompletedAt, st.UpdatedAt = utcPtr(sendingAt), utcPtr(completed), st.UpdatedAt.UTC()

	rows, err := s.db.Query(ctx, `
		SELECT channel, status, count(*) FROM deliveries WHERE campaign_id = $1 GROUP BY channel, status`, id)
	if err != nil {
		return campaign.CampaignStatus{}, fmt.Errorf("postgres: campaign status: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var ch, status string
		var n int
		if err := rows.Scan(&ch, &status, &n); err != nil {
			return campaign.CampaignStatus{}, fmt.Errorf("postgres: campaign status: %w", err)
		}
		c := &st.ByChannel.SMS
		if ch == "email" {
			c = &st.ByChannel.Email
		}
		switch status {
		case "queued":
			c.Queued += n
		case "sending":
			c.Sending += n
		case "sent":
			c.Sent += n
		case "failed":
			c.Failed += n
		}
	}
	if err := rows.Err(); err != nil {
		return campaign.CampaignStatus{}, fmt.Errorf("postgres: campaign status: %w", err)
	}
	// total = queued + sending + sent + failed per channel; counts = sms + email (REQ-TRK-01).
	for _, c := range []*campaign.StatusCounts{&st.ByChannel.SMS, &st.ByChannel.Email} {
		c.Total = c.Queued + c.Sending + c.Sent + c.Failed
	}
	sms, email := st.ByChannel.SMS, st.ByChannel.Email
	st.Counts = campaign.StatusCounts{
		Total: sms.Total + email.Total, Queued: sms.Queued + email.Queued, Sending: sms.Sending + email.Sending,
		Sent: sms.Sent + email.Sent, Failed: sms.Failed + email.Failed,
	}
	return st, nil
}

const deliveryColumns = `id::text, campaign_id::text, content_version_id::text, customer_id::text, channel, status,
	attempts, failed_attempts, rate_limited_count, retry_count, next_attempt_at, first_attempt_at, last_attempt_at,
	last_http_status, last_error, failure_reason, provider_message_id, sent_at, failed_at, created_at, updated_at`

// ListDeliveries returns one page of the campaign's deliveries. No recipient address is selected:
// deliveries hold none and customers is not joined (REQ-SEC-01 spirit).
func (s *CampaignStore) ListDeliveries(ctx context.Context, id string, f campaign.DeliveryFilter) ([]campaign.Delivery, int, error) {
	if err := s.campaignExists(ctx, id); err != nil {
		return nil, 0, err
	}
	where := `WHERE campaign_id = $1 AND ($2 = '' OR status = $2) AND ($3 = '' OR channel = $3)
		AND ($4 = '' OR customer_id = nullif($4, '')::uuid)`
	args := []any{id, f.Status, f.Channel, f.CustomerID}
	var total int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM deliveries `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count deliveries: %w", err)
	}
	order := `customer_id, channel`
	if f.Order == campaign.DeliveryOrderRecent {
		order = `updated_at DESC, id`
	}
	rows, err := s.db.Query(ctx, `SELECT `+deliveryColumns+` FROM deliveries `+where+`
		ORDER BY `+order+` LIMIT $5 OFFSET $6`, append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list deliveries: %w", err)
	}
	defer rows.Close()
	out := []campaign.Delivery{}
	for rows.Next() {
		var d campaign.Delivery
		if err := rows.Scan(&d.ID, &d.CampaignID, &d.ContentVersionID, &d.CustomerID, &d.Channel, &d.Status,
			&d.Attempts, &d.FailedAttempts, &d.RateLimitedCount, &d.RetryCount, &d.NextAttemptAt, &d.FirstAttemptAt,
			&d.LastAttemptAt, &d.LastHTTPStatus, &d.LastError, &d.FailureReason, &d.ProviderMessageID, &d.SentAt,
			&d.FailedAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("postgres: list deliveries: %w", err)
		}
		d.NextAttemptAt, d.CreatedAt, d.UpdatedAt = d.NextAttemptAt.UTC(), d.CreatedAt.UTC(), d.UpdatedAt.UTC()
		d.FirstAttemptAt, d.LastAttemptAt = utcPtr(d.FirstAttemptAt), utcPtr(d.LastAttemptAt)
		d.SentAt, d.FailedAt = utcPtr(d.SentAt), utcPtr(d.FailedAt)
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("postgres: list deliveries: %w", err)
	}
	return out, total, nil
}
