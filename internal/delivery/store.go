package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/gulcint/smart-campaign-composer/internal/content"
)

type Channel string

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
)

type Status string

const (
	StatusQueued  Status = "queued"
	StatusSending Status = "sending"
	StatusSent    Status = "sent"
	StatusFailed  Status = "failed"
)

type FailureReason string

const (
	FailureProviderRejected     FailureReason = "provider_rejected"           // non-retryable 4xx / 1xx / 3xx
	FailureMaxAttempts          FailureReason = "max_attempts_exceeded"       // 5th failed attempt
	FailureRateLimitBudget      FailureReason = "rate_limit_budget_exhausted" // 20th 429 or 10-minute window
	FailureContentTooLong       FailureReason = "content_too_long"            // send-time render check
	FailureRecipientUnavailable FailureReason = "recipient_unavailable"       // contact/consent gone at send time
)

// Advisory lock keys (pg_try_advisory_lock), one dispatcher per channel.
const (
	LockKeySMS   int64 = 7001
	LockKeyEmail int64 = 7002
)

var (
	ErrLeaseLost     = errors.New("delivery: lease lost")
	ErrInvalidConfig = errors.New("delivery: invalid dispatcher config")
)

// Job is one claimed delivery.
type Job struct {
	ID             string // delivery id == Idempotency-Key
	CampaignID     string
	CustomerID     string
	Channel        Channel
	To             string // CURRENT phone (sms) or email (email) re-read from customers at claim time; "" if NULL. NEVER logged
	Consent        bool   // CURRENT channel opt-in (sms_opt_in / email_opt_in) re-read at claim time
	Recipient      content.Recipient
	Content        content.Content // approved version template
	Attempts       int             // after this claim
	FailedAttempts int
	RateLimited    int
	FirstAttemptAt time.Time
}

type SentUpdate struct {
	At                time.Time
	HTTPStatus        int
	ProviderMessageID string
}

type RetryUpdate struct {
	At            time.Time
	NextAttemptAt time.Time
	Count         CountKind
	HTTPStatus    int    // 0 for transport errors
	LastError     string // http_<status>, timeout, connection_error
}

type FailedUpdate struct {
	At         time.Time
	Reason     FailureReason
	Count      CountKind
	HTTPStatus int
	LastError  string
}

// Store is the queue (implemented by postgres.NewDeliveryStore; fakeable).
type Store interface {
	// TryLockChannel takes the channel's advisory lock on a dedicated connection.
	TryLockChannel(ctx context.Context, ch Channel) (release func(), acquired bool, err error)
	// Claim moves up to limit due queued rows (next_attempt_at <= now) to sending with
	// lease_expires_at = leaseUntil, using FOR UPDATE SKIP LOCKED.
	Claim(ctx context.Context, ch Channel, now time.Time, limit int, leaseUntil time.Time) ([]Job, error)
	// ReclaimExpired returns sending rows whose lease expired before now to queued.
	ReclaimExpired(ctx context.Context, ch Channel, now time.Time) (int, error)
	// Mark* update a row currently in sending; ErrLeaseLost if it is not. Each also completes
	// the campaign in the same transaction when no queued/sending delivery remains.
	MarkSent(ctx context.Context, id string, u SentUpdate) error
	ScheduleRetry(ctx context.Context, id string, u RetryUpdate) error
	MarkFailed(ctx context.Context, id string, u FailedUpdate) error
}
