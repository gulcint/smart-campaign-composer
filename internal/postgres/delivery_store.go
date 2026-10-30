package postgres

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// storeTimeout bounds lock bookkeeping that must not depend on the caller's context.
const storeTimeout = 5 * time.Second

// TryLockChannel takes the session advisory lock of ch on a connection reserved for as long as
// the lock is held.
func (s deliveryStore) TryLockChannel(ctx context.Context, ch delivery.Channel) (delivery.ChannelLock, bool, error) {
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
	return &channelLock{conn: conn, key: key}, true, nil
}

// channelLock is used by one goroutine at a time (the dispatcher's Run loop).
type channelLock struct {
	conn *pgxpool.Conn
	key  int64
	once sync.Once
}

// Held asks the lock's own session whether it still holds the advisory lock. A terminated
// backend, a network failure or a database restart surfaces here as an error.
func (l *channelLock) Held(ctx context.Context) (bool, error) {
	var held bool
	err := l.conn.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM pg_locks
			WHERE locktype = 'advisory' AND pid = pg_backend_pid() AND granted
				AND classid = 0::oid AND objid = $1::bigint::oid AND objsubid = 1)`, l.key).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("postgres: check channel lock: %w", err)
	}
	return held, nil
}

func (l *channelLock) Release() {
	l.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), storeTimeout)
		defer cancel()
		if _, err := l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, l.key); err != nil {
			// The lock may still be held by this session: never hand it back to the pool.
			_ = l.conn.Conn().Close(ctx)
		}
		l.conn.Release()
	})
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
			cu.first_name, cu.last_name, cu.city, cu.language,
			cv.sms_body, cv.email_subject, cv.email_body,
			cv.en_sms_body, cv.en_email_subject, cv.en_email_body,
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
		var to, enSMS, enSubject, enBody *string
		if err := rows.Scan(&j.ID, &j.CampaignID, &j.CustomerID, &to, &j.Consent,
			&j.Recipient.FirstName, &j.Recipient.LastName, &j.Recipient.City, &j.Recipient.Language,
			&j.Content.SMS.Body, &j.Content.Email.Subject, &j.Content.Email.Body,
			&enSMS, &enSubject, &enBody,
			&j.Attempts, &j.FailedAttempts, &j.RateLimited, &j.FirstAttemptAt); err != nil {
			return nil, fmt.Errorf("postgres: claim deliveries: %w", err)
		}
		if to != nil {
			j.To = *to
		}
		j.Content.EN = englishOf(enSMS, enSubject, enBody) // P8: the language version is picked at render time
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

// Requeue returns claimed but never started jobs to queued without touching their counters.
func (s deliveryStore) Requeue(ctx context.Context, jobs []delivery.Job, now time.Time) (int, error) {
	if len(jobs) == 0 {
		return 0, nil
	}
	ids := make([]string, len(jobs))
	attempts := make([]int32, len(jobs))
	for i, j := range jobs {
		ids[i] = j.ID
		attempts[i] = int32(j.Attempts) //nolint:gosec // claim counts are small
	}
	tag, err := s.db.Exec(ctx, `
		UPDATE deliveries d SET status = 'queued', lease_expires_at = NULL, updated_at = $3
		FROM unnest($1::uuid[], $2::int[]) AS j(id, attempts)
		WHERE d.id = j.id AND d.status = 'sending' AND d.attempts = j.attempts`,
		ids, attempts, now)
	if err != nil {
		return 0, fmt.Errorf("postgres: requeue deliveries: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// fenced is the WHERE clause of every Mark* update: the row is still in sending under the claim
// that produced the update ($2 = Job.Attempts; 0 = not fenced).
const fenced = `WHERE id = $1 AND status = 'sending' AND ($2 = 0 OR attempts = $2)`

func (s deliveryStore) MarkSent(ctx context.Context, id string, u delivery.SentUpdate) error {
	return s.finish(ctx, "mark sent", u.At, query{`
		UPDATE deliveries SET
			status = 'sent', sent_at = $3, last_http_status = $4, provider_message_id = NULLIF($5, ''),
			lease_expires_at = NULL, updated_at = $3
		` + fenced + ` RETURNING campaign_id`,
		[]any{id, u.Attempts, u.At, u.HTTPStatus, u.ProviderMessageID}}, nil)
}

func (s deliveryStore) ScheduleRetry(ctx context.Context, id string, u delivery.RetryUpdate) error {
	reason := delivery.FailureMaxAttempts
	if u.Count == delivery.CountRateLimited {
		reason = delivery.FailureRateLimitBudget
	}
	return s.finish(ctx, "schedule retry", u.At, query{`
		UPDATE deliveries SET
			status = 'queued', next_attempt_at = $4, lease_expires_at = NULL,
			retry_count = retry_count + 1,
			failed_attempts = failed_attempts + $5, rate_limited_count = rate_limited_count + $6,
			last_http_status = COALESCE(NULLIF($7, 0), last_http_status), last_error = $8, updated_at = $3
		` + fenced + ` RETURNING campaign_id`,
		[]any{id, u.Attempts, u.At, u.NextAttemptAt, failureInc(u.Count), rateLimitedInc(u.Count), u.HTTPStatus, u.LastError}},
		failWithoutCount(id, u.Attempts, u.At, reason, u.HTTPStatus, u.LastError))
}

func (s deliveryStore) MarkFailed(ctx context.Context, id string, u delivery.FailedUpdate) error {
	return s.finish(ctx, "mark failed", u.At, query{`
		UPDATE deliveries SET
			status = 'failed', failure_reason = $4, failed_at = $3, lease_expires_at = NULL,
			failed_attempts = failed_attempts + $5, rate_limited_count = rate_limited_count + $6,
			last_http_status = COALESCE(NULLIF($7, 0), last_http_status), last_error = NULLIF($8, ''), updated_at = $3
		` + fenced + ` RETURNING campaign_id`,
		[]any{id, u.Attempts, u.At, string(u.Reason), failureInc(u.Count), rateLimitedInc(u.Count), u.HTTPStatus, u.LastError}},
		failWithoutCount(id, u.Attempts, u.At, u.Reason, u.HTTPStatus, u.LastError))
}

type query struct {
	sql  string
	args []any
}

// failWithoutCount is the fallback when a counter increment hits a deliveries CHECK (failed_attempts
// <= 5, rate_limited_count <= 20), e.g. after a retry budget above the schema's bounds: the row is
// failed with its counters unchanged instead of being left in sending to be reclaimed forever.
func failWithoutCount(id string, attempts int, at time.Time, reason delivery.FailureReason, status int, lastError string) *query {
	return &query{`
		UPDATE deliveries SET
			status = 'failed', failure_reason = $4, failed_at = $3, lease_expires_at = NULL,
			last_http_status = COALESCE(NULLIF($5, 0), last_http_status), last_error = NULLIF($6, ''), updated_at = $3
		` + fenced + ` RETURNING campaign_id`,
		[]any{id, attempts, at, string(reason), status, lastError}}
}

// finish runs one delivery update (which must RETURN campaign_id) and, in the same transaction,
// completes the campaign when no queued/sending delivery remains (completeCampaignIfDone). If the
// update violates a CHECK constraint (SQLSTATE 23514) and onCheck is set, onCheck runs instead,
// in a new transaction.
func (s deliveryStore) finish(ctx context.Context, op string, at time.Time, q query, onCheck *query) error {
	err := s.update(ctx, at, q)
	var pgErr *pgconn.PgError
	if onCheck != nil && errors.As(err, &pgErr) && pgErr.Code == "23514" { // check_violation
		err = s.update(ctx, at, *onCheck)
	}
	if err != nil && !errors.Is(err, delivery.ErrLeaseLost) {
		return fmt.Errorf("postgres: %s: %w", op, err)
	}
	return err
}

func (s deliveryStore) update(ctx context.Context, at time.Time, q query) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	var campaignID string
	if err := tx.QueryRow(ctx, q.sql, q.args...).Scan(&campaignID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return delivery.ErrLeaseLost
		}
		return err
	}
	// Shared with the send acceptance (send.go): locks the campaign row, then completes it if no
	// delivery is queued or sending. After a retry the row is queued again, so it is a no-op.
	if _, err := completeCampaignIfDone(ctx, tx, campaignID, at); err != nil {
		return err
	}
	return tx.Commit(ctx)
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
