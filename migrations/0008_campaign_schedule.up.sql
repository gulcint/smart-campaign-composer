-- Scheduled send (db-schema.md §3.4, §3.4.1, §3.9; D62): a new campaign state `scheduled`
-- (approved + planned send, no delivery yet) and the planned instant campaigns.scheduled_at.
-- 0003 is not edited (migration immutability): its constraints and trigger function are replaced.

ALTER TABLE campaigns ADD COLUMN scheduled_at timestamptz;

ALTER TABLE campaigns
DROP CONSTRAINT campaigns_state_check,
ADD CONSTRAINT campaigns_state_check CHECK (
    state IN ('draft', 'audience_ready', 'content_ready', 'approved', 'scheduled', 'sending', 'completed')
),
DROP CONSTRAINT campaigns_approval_present_check,
ADD CONSTRAINT campaigns_approval_present_check CHECK (
    (state IN ('approved', 'scheduled', 'sending', 'completed')) = (approved_content_version_id IS NOT NULL)
),
-- scheduled => set; draft..approved => empty; free in sending/completed (kept when the worker
-- started the send from a plan, NULL after an immediate send or "send now": the writes decide).
ADD CONSTRAINT campaigns_scheduled_at_check CHECK (
    (state = 'scheduled' AND scheduled_at IS NOT NULL)
    OR state IN ('sending', 'completed')
    OR (state IN ('draft', 'audience_ready', 'content_ready', 'approved') AND scheduled_at IS NULL)
);

-- The 0003 transitions plus approved -> scheduled, scheduled -> approved (cancel) and
-- scheduled -> sending (due, or "send now"). No way back from scheduled to audience_ready or
-- content_ready: a plan freezes segment and content.
CREATE OR REPLACE FUNCTION campaigns_state_transition() RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.state = OLD.state THEN
        RETURN NEW;
    END IF;
    IF (OLD.state, NEW.state) IN (
        ('draft', 'audience_ready'),
        ('audience_ready', 'content_ready'),
        ('content_ready', 'audience_ready'),
        ('content_ready', 'approved'),
        ('approved', 'audience_ready'),
        ('approved', 'content_ready'),
        ('approved', 'scheduled'),
        ('approved', 'sending'),
        ('scheduled', 'approved'),
        ('scheduled', 'sending'),
        ('sending', 'completed')
    ) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'campaign state transition % -> % is not allowed', OLD.state, NEW.state
        USING ERRCODE = 'check_violation';
END;
$$;

-- The scheduler's due query scans only planned campaigns.
CREATE INDEX campaigns_scheduled_due_idx ON campaigns (scheduled_at) WHERE state = 'scheduled';
