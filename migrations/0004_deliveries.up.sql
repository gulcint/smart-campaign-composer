-- The send queue itself: one row per (campaign, channel, customer) (db-schema.md §3.7, §4).
-- No recipient address is stored here; the worker reads it from customers at send time.

CREATE TABLE deliveries (
    id uuid NOT NULL,
    campaign_id uuid NOT NULL,
    content_version_id uuid NOT NULL,
    customer_id uuid NOT NULL,
    channel text NOT NULL,
    status text NOT NULL DEFAULT 'queued',
    attempts integer NOT NULL DEFAULT 0,
    failed_attempts integer NOT NULL DEFAULT 0,
    rate_limited_count integer NOT NULL DEFAULT 0,
    retry_count integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    lease_expires_at timestamptz,
    first_attempt_at timestamptz,
    last_attempt_at timestamptz,
    last_http_status integer,
    last_error text,
    failure_reason text,
    provider_message_id text,
    sent_at timestamptz,
    failed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT deliveries_pkey PRIMARY KEY (id),
    CONSTRAINT deliveries_campaign_id_fkey FOREIGN KEY (campaign_id) REFERENCES campaigns (id),
    CONSTRAINT deliveries_content_version_id_fkey FOREIGN KEY (content_version_id)
    REFERENCES content_versions (id),
    CONSTRAINT deliveries_customer_id_fkey FOREIGN KEY (customer_id) REFERENCES customers (id),
    CONSTRAINT deliveries_campaign_channel_customer_key UNIQUE (campaign_id, channel, customer_id),
    CONSTRAINT deliveries_channel_check CHECK (channel IN ('sms', 'email')),
    CONSTRAINT deliveries_status_check CHECK (status IN ('queued', 'sending', 'sent', 'failed')),
    CONSTRAINT deliveries_attempts_check CHECK (attempts >= 0 AND retry_count >= 0),
    CONSTRAINT deliveries_failed_attempts_check CHECK (failed_attempts BETWEEN 0 AND 5),
    CONSTRAINT deliveries_rate_limited_count_check CHECK (rate_limited_count BETWEEN 0 AND 20),
    CONSTRAINT deliveries_last_error_check CHECK (
        last_error IS NULL
        OR last_error ~ '^(http_[0-9]{3}|timeout|connection_error|content_too_long|recipient_unavailable)$'
    ),
    CONSTRAINT deliveries_failure_reason_check CHECK (
        failure_reason IN (
            'provider_rejected',
            'max_attempts_exceeded',
            'rate_limit_budget_exhausted',
            'content_too_long',
            'recipient_unavailable'
        )
    ),
    CONSTRAINT deliveries_sent_check CHECK ((status = 'sent') = (sent_at IS NOT NULL)),
    CONSTRAINT deliveries_failed_check CHECK (
        (status = 'failed') = (failure_reason IS NOT NULL AND failed_at IS NOT NULL)
    ),
    CONSTRAINT deliveries_lease_check CHECK (status <> 'sending' OR lease_expires_at IS NOT NULL)
);

CREATE INDEX deliveries_claim_idx ON deliveries (channel, next_attempt_at) WHERE status = 'queued';
CREATE INDEX deliveries_lease_idx ON deliveries (lease_expires_at) WHERE status = 'sending';
CREATE INDEX deliveries_campaign_status_idx ON deliveries (campaign_id, status);
