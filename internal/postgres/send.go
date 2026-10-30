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

// CreateDeliveries accepts a send under the campaign row lock (campaign.Store). From scheduled
// ("send now", D62) the plan is dropped: scheduled_at = NULL.
func (s *CampaignStore) CreateDeliveries(ctx context.Context, id, versionID string, now func() time.Time, check func(campaign.LockedCampaign) error) (campaign.DeliveryTotals, campaign.State, error) {
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
	// Read the clock only now that the row lock is held: a concurrent write to this campaign has
	// committed with an earlier time, so updated_at never moves backwards.
	t, state, err := startSendingLocked(ctx, tx, id, versionID, lc, now(), nil)
	if err != nil {
		return campaign.DeliveryTotals{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return campaign.DeliveryTotals{}, "", fmt.Errorf("postgres: send: %w", err)
	}
	return t, state, nil
}

// StartDueCampaign claims and starts one due plan in its own transaction (campaign.Store;
// db-schema.md §3.9). SKIP LOCKED lets concurrent schedulers (several worker processes) take
// different campaigns; the caller's check re-tests due-ness on the row re-read under the lock, so
// a campaign re-planned, cancelled or started by another transaction meanwhile is skipped. The
// UNIQUE (campaign_id, channel, customer_id) key stays the last guard against a second start.
func (s *CampaignStore) StartDueCampaign(ctx context.Context, now time.Time, exclude []string, check func(campaign.LockedCampaign) error) (campaign.ScheduledStart, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return campaign.ScheduledStart{}, fmt.Errorf("postgres: start scheduled send: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	if exclude == nil {
		exclude = []string{} // NULL would make the ANY() filter drop every row
	}
	var id string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM campaigns
		WHERE state = 'scheduled' AND scheduled_at <= $1 AND NOT (id::text = ANY($2::text[]))
		ORDER BY scheduled_at, id
		LIMIT 1
		FOR UPDATE SKIP LOCKED`, now, exclude).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.ScheduledStart{}, nil
	}
	if err != nil {
		return campaign.ScheduledStart{}, fmt.Errorf("postgres: claim scheduled campaign: %w", err)
	}
	st := campaign.ScheduledStart{CampaignID: id}
	// The row is locked; this re-read (a new statement, a fresh snapshot) sees every write that
	// committed before the lock was granted.
	lc, err := lockCampaign(ctx, tx, id)
	if err != nil {
		return st, err
	}
	if err := check(lc.LockedCampaign); err != nil {
		return st, err
	}
	// Defensive: scheduled implies an approved version (campaigns_approval_present_check).
	if lc.ApprovedVersionID == nil || lc.ScheduledAt == nil {
		return st, fmt.Errorf("%w: scheduled campaign without approval or plan", campaign.ErrInvalidState)
	}
	t, state, err := startSendingLocked(ctx, tx, id, *lc.ApprovedVersionID, lc, now, lc.ScheduledAt)
	if err != nil {
		return st, err
	}
	if err := tx.Commit(ctx); err != nil {
		return st, fmt.Errorf("postgres: start scheduled send: %w", err)
	}
	st.ScheduledAt, st.StartedAt, st.State, st.Deliveries = *lc.ScheduledAt, now, state, t
	return st, nil
}

// startSendingLocked is the one send start shared by the immediate send (CreateDeliveries) and
// the Scheduler (StartDueCampaign), so both follow the same rule (go-interfaces
// startSendingLocked). It runs inside the caller's transaction on the campaign row it holds locked
// (lc, already checked by the caller) and writes: one queued delivery (next_attempt_at = at) per
// (channel, customer) of the channel audiences of the stored rule at the stored reference time
// with the CURRENT consent and contact data (segment-dsl.md §6, REQ-AUD-04), state sending,
// sending_started_at = at, scheduled_at = scheduledAt (the caller decides: NULL for Send, the kept
// plan for the Scheduler) and, when no delivery was created, completed. It returns the created
// counts and the resulting state.
func startSendingLocked(ctx context.Context, tx pgx.Tx, id, versionID string, lc lockedCampaign, at time.Time, scheduledAt *time.Time) (campaign.DeliveryTotals, campaign.State, error) {
	// Defensive: approved/scheduled (checked by the caller) implies a stored segment
	// (campaigns_segment_present_check).
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
		UPDATE campaigns SET state = 'sending', sending_started_at = $2, scheduled_at = $3, updated_at = $2
		WHERE id = $1`,
		id, at, scheduledAt); err != nil {
		return campaign.DeliveryTotals{}, "", fmt.Errorf("postgres: start sending: %w", err)
	}
	state := campaign.StateSending
	// Both audiences empty: the campaign is complete in the same transaction (approved|scheduled ->
	// sending -> completed; the state trigger has no direct way to completed).
	done, err := completeCampaignIfDone(ctx, tx, id, at)
	if err != nil {
		return campaign.DeliveryTotals{}, "", err
	}
	if done {
		state = campaign.StateCompleted
	}
	return t, state, nil
}

// ScheduleSend plans (or re-plans) the send under the campaign row lock (campaign.Store).
func (s *CampaignStore) ScheduleSend(ctx context.Context, id string, at time.Time, now func() time.Time, check func(campaign.LockedCampaign) error) error {
	return s.writeSchedule(ctx, id, campaign.StateScheduled, &at, now, check)
}

// CancelSchedule drops the plan under the campaign row lock (campaign.Store).
func (s *CampaignStore) CancelSchedule(ctx context.Context, id string, now func() time.Time, check func(campaign.LockedCampaign) error) error {
	return s.writeSchedule(ctx, id, campaign.StateApproved, nil, now, check)
}

// writeSchedule moves a checked, locked campaign to state with scheduled_at. Only the campaign row
// changes: the approval, the approved version and the deliveries are untouched.
func (s *CampaignStore) writeSchedule(ctx context.Context, id string, state campaign.State, scheduledAt *time.Time, now func() time.Time, check func(campaign.LockedCampaign) error) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: write schedule: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	lc, err := lockCampaign(ctx, tx, id)
	if err != nil {
		return err
	}
	if err := check(lc.LockedCampaign); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE campaigns SET state = $2, scheduled_at = $3, updated_at = $4 WHERE id = $1`,
		id, string(state), scheduledAt, now()); err != nil {
		return fmt.Errorf("postgres: write schedule: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: write schedule: %w", err)
	}
	return nil
}

// completeCampaignIfDone moves a sending campaign to completed when none of its deliveries is
// queued or sending (db-schema.md §4 "Tamamlama"). It runs inside the caller's transaction: the
// send acceptance calls it (no delivery at all), and every delivery.Store write that moves a
// delivery to sent or failed must call it in the same transaction, after that write. It reports
// whether the campaign was completed by this call.
//
// It first locks the campaign row (a no-op when the caller already holds it), then checks in a
// SEPARATE statement. Under READ COMMITTED each statement takes a fresh snapshot after the lock is
// granted, so of two transactions finishing the last SMS and the last email delivery concurrently
// the second one to get the lock sees the first one's committed delivery and completes the
// campaign. In one statement both could see the other's delivery still sending and neither would
// complete it.
func completeCampaignIfDone(ctx context.Context, tx pgx.Tx, campaignID string, now time.Time) (bool, error) {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM campaigns WHERE id = $1 FOR UPDATE`, campaignID); err != nil {
		return false, fmt.Errorf("postgres: complete campaign: %w", err)
	}
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

// readSnapshot is a read-only REPEATABLE READ transaction: every statement in it sees the same
// snapshot, so values read by several queries agree with each other.
var readSnapshot = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}

// DeliveryStatus reads the campaign's delivery counters (openapi CampaignStatus).
func (s *CampaignStore) DeliveryStatus(ctx context.Context, id string) (campaign.CampaignStatus, error) {
	var (
		st                   campaign.CampaignStatus
		state                string
		scheduledAt          *time.Time
		sendingAt, completed *time.Time
	)
	// One snapshot for the campaign row and the counters: a response never pairs a state with
	// counters from another moment (e.g. approved with non-zero counters).
	tx, err := s.db.BeginTx(ctx, readSnapshot)
	if err != nil {
		return campaign.CampaignStatus{}, fmt.Errorf("postgres: campaign status: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // read-only: nothing to commit

	err = tx.QueryRow(ctx, `
		SELECT state, scheduled_at, sending_started_at, completed_at,
			greatest(updated_at, (SELECT max(updated_at) FROM deliveries WHERE campaign_id = $1))
		FROM campaigns WHERE id = $1`, id).Scan(&state, &scheduledAt, &sendingAt, &completed, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return campaign.CampaignStatus{}, campaign.ErrCampaignNotFound
	}
	if err != nil {
		return campaign.CampaignStatus{}, fmt.Errorf("postgres: campaign status: %w", err)
	}
	st.CampaignID, st.State = id, campaign.State(state)
	st.ScheduledAt = utcPtr(scheduledAt)
	st.SendingStartedAt, st.CompletedAt, st.UpdatedAt = utcPtr(sendingAt), utcPtr(completed), st.UpdatedAt.UTC()

	rows, err := tx.Query(ctx, `
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
	// Existence, count and page in one snapshot, so total and items agree.
	tx, err := s.db.BeginTx(ctx, readSnapshot)
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: list deliveries: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // read-only: nothing to commit

	var one int
	err = tx.QueryRow(ctx, `SELECT 1 FROM campaigns WHERE id = $1`, id).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, campaign.ErrCampaignNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("postgres: read campaign: %w", err)
	}
	where := `WHERE campaign_id = $1 AND ($2 = '' OR status = $2) AND ($3 = '' OR channel = $3)
		AND ($4 = '' OR customer_id = nullif($4, '')::uuid)`
	args := []any{id, f.Status, f.Channel, f.CustomerID}
	var total int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM deliveries `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("postgres: count deliveries: %w", err)
	}
	order := `customer_id, channel`
	if f.Order == campaign.DeliveryOrderRecent {
		order = `updated_at DESC, id`
	}
	rows, err := tx.Query(ctx, `SELECT `+deliveryColumns+` FROM deliveries `+where+`
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
