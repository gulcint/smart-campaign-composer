package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/gulcint/smart-campaign-composer/internal/delivery"
)

// NewDeliveryStore returns the PostgreSQL queue of the delivery worker (db-schema.md §3.7, §4).
func NewDeliveryStore(db *pgxpool.Pool) delivery.Store {
	return deliveryStore{db: db}
}

type deliveryStore struct{ db *pgxpool.Pool }

func lockKey(ch delivery.Channel) int64 {
	if ch == delivery.ChannelEmail {
		return delivery.LockKeyEmail
	}
	return delivery.LockKeySMS
}

// TryLockChannel takes the session advisory lock of ch on a connection reserved for as long as
// the lock is held; release unlocks it and returns the connection to the pool.
func (s deliveryStore) TryLockChannel(ctx context.Context, ch delivery.Channel) (func(), bool, error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("postgres: lock channel: %w", err)
	}
	key := lockKey(ch)
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("postgres: lock channel: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, false, nil
	}
	release := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, key); err != nil {
			// The lock may still be held by this session: never hand it back to the pool.
			_ = conn.Conn().Close(ctx)
		}
		conn.Release()
	}
	return release, true, nil
}

// Claim re-reads the customer's current contact and consent and the approved content in the same
// statement that moves the rows to sending.
func (s deliveryStore) Claim(ctx context.Context, ch delivery.Channel, now time.Time, limit int, leaseUntil time.Time) ([]delivery.Job, error) {
	rows, err := s.db.Query(ctx, `
		WITH picked AS (
			SELECT id FROM deliveries
			WHERE channel = $1 AND status = 'queued' AND next_attempt_at <= $2
			ORDER BY next_attempt_at, id
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE deliveries d SET
				status = 'sending', lease_expires_at = $4, attempts = d.attempts + 1,
				last_attempt_at = $2, first_attempt_at = COALESCE(d.first_attempt_at, $2), updated_at = $2
			FROM picked WHERE d.id = picked.id
			RETURNING d.id, d.campaign_id, d.customer_id, d.content_version_id, d.next_attempt_at,
				d.attempts, d.failed_attempts, d.rate_limited_count, d.first_attempt_at
		)
		SELECT c.id::text, c.campaign_id::text, c.customer_id::text,
			CASE WHEN $1 = 'sms' THEN cu.phone ELSE cu.email END,
			CASE WHEN $1 = 'sms' THEN cu.sms_opt_in ELSE cu.email_opt_in END,
			cu.first_name, cu.last_name, cu.city,
			cv.sms_body, cv.email_subject, cv.email_body,
			c.attempts, c.failed_attempts, c.rate_limited_count, c.first_attempt_at
		FROM claimed c
		JOIN customers cu ON cu.id = c.customer_id
		JOIN content_versions cv ON cv.id = c.content_version_id
		ORDER BY c.next_attempt_at, c.id`,
		string(ch), now, limit, leaseUntil)
	if err != nil {
		return nil, fmt.Errorf("postgres: claim deliveries: %w", err)
	}
	defer rows.Close()
	var jobs []delivery.Job
	for rows.Next() {
		j := delivery.Job{Channel: ch}
		var to *string
		if err := rows.Scan(&j.ID, &j.CampaignID, &j.CustomerID, &to, &j.Consent,
			&j.Recipient.FirstName, &j.Recipient.LastName, &j.Recipient.City,
			&j.Content.SMS.Body, &j.Content.Email.Subject, &j.Content.Email.Body,
			&j.Attempts, &j.FailedAttempts, &j.RateLimited, &j.FirstAttemptAt); err != nil {
			return nil, fmt.Errorf("postgres: claim deliveries: %w", err)
		}
		if to != nil {
			j.To = *to
		}
		j.FirstAttemptAt = j.FirstAttemptAt.UTC()
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: claim deliveries: %w", err)
	}
	return jobs, nil
}

// ReclaimExpired returns rows whose lease expired before now to queued; failed_attempts is
// unchanged and the next send reuses the same Idempotency-Key (the delivery id).
func (s deliveryStore) ReclaimExpired(ctx context.Context, ch delivery.Channel, now time.Time) (int, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE deliveries SET status = 'queued', lease_expires_at = NULL, updated_at = $2
		WHERE channel = $1 AND status = 'sending' AND lease_expires_at < $2`,
		string(ch), now)
	if err != nil {
		return 0, fmt.Errorf("postgres: reclaim expired deliveries: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s deliveryStore) MarkSent(ctx context.Context, id string, u delivery.SentUpdate) error {
	return s.finish(ctx, "mark sent", u.At, true, `
		UPDATE deliveries SET
			status = 'sent', sent_at = $2, last_http_status = $3, provider_message_id = NULLIF($4, ''),
			lease_expires_at = NULL, updated_at = $2
		WHERE id = $1 AND status = 'sending'
		RETURNING campaign_id`,
		id, u.At, u.HTTPStatus, u.ProviderMessageID)
}

func (s deliveryStore) ScheduleRetry(ctx context.Context, id string, u delivery.RetryUpdate) error {
	return s.finish(ctx, "schedule retry", u.At, false, `
		UPDATE deliveries SET
			status = 'queued', next_attempt_at = $3, lease_expires_at = NULL,
			retry_count = retry_count + 1,
			failed_attempts = failed_attempts + $4, rate_limited_count = rate_limited_count + $5,
			last_http_status = COALESCE(NULLIF($6, 0), last_http_status), last_error = $7, updated_at = $2
		WHERE id = $1 AND status = 'sending'
		RETURNING campaign_id`,
		id, u.At, u.NextAttemptAt, failureInc(u.Count), rateLimitedInc(u.Count), u.HTTPStatus, u.LastError)
}

func (s deliveryStore) MarkFailed(ctx context.Context, id string, u delivery.FailedUpdate) error {
	return s.finish(ctx, "mark failed", u.At, true, `
		UPDATE deliveries SET
			status = 'failed', failure_reason = $3, failed_at = $2, lease_expires_at = NULL,
			failed_attempts = failed_attempts + $4, rate_limited_count = rate_limited_count + $5,
			last_http_status = COALESCE(NULLIF($6, 0), last_http_status), last_error = NULLIF($7, ''), updated_at = $2
		WHERE id = $1 AND status = 'sending'
		RETURNING campaign_id`,
		id, u.At, string(u.Reason), failureInc(u.Count), rateLimitedInc(u.Count), u.HTTPStatus, u.LastError)
}

// finish runs one delivery update (which must RETURN campaign_id) and, for a terminal status,
// completes the campaign in the same transaction when no queued/sending delivery remains.
func (s deliveryStore) finish(ctx context.Context, op string, at time.Time, terminal bool, sql string, args ...any) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: %s: %w", op, err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	var campaignID string
	if err := tx.QueryRow(ctx, sql, args...).Scan(&campaignID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return delivery.ErrLeaseLost
		}
		return fmt.Errorf("postgres: %s: %w", op, err)
	}
	if terminal {
		if err := completeCampaign(ctx, tx, campaignID, at); err != nil {
			return fmt.Errorf("postgres: %s: %w", op, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: %s: %w", op, err)
	}
	return nil
}

// completeCampaign moves a sending campaign to completed when none of its deliveries is queued or
// sending. The campaign row is locked first, in its own statement, so that the NOT EXISTS check
// runs on a snapshot taken after any concurrent finisher of the same campaign has committed;
// otherwise two last deliveries finishing at once could each see the other still sending.
func completeCampaign(ctx context.Context, tx pgx.Tx, campaignID string, at time.Time) error {
	if _, err := tx.Exec(ctx, `SELECT 1 FROM campaigns WHERE id = $1 FOR UPDATE`, campaignID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE campaigns SET state = 'completed', completed_at = $2, updated_at = $2
		WHERE id = $1 AND state = 'sending'
			AND NOT EXISTS (SELECT 1 FROM deliveries WHERE campaign_id = $1 AND status IN ('queued', 'sending'))`,
		campaignID, at)
	return err
}

func failureInc(c delivery.CountKind) int {
	if c == delivery.CountFailure {
		return 1
	}
	return 0
}

func rateLimitedInc(c delivery.CountKind) int {
	if c == delivery.CountRateLimited {
		return 1
	}
	return 0
}
