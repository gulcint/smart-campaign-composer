-- allow-destructive: reverts 0008; the plan of sending/completed campaigns is lost (db-schema.md §3.4.1).

-- Planned campaigns go back to approved while the 0008 trigger still allows it; approvals stay.
UPDATE campaigns SET state = 'approved', scheduled_at = NULL
WHERE state = 'scheduled';

DROP INDEX campaigns_scheduled_due_idx;
ALTER TABLE campaigns DROP CONSTRAINT campaigns_scheduled_at_check;

-- The 0003 body (0004..0007 do not touch the function).
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
        ('approved', 'sending'),
        ('sending', 'completed')
    ) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'campaign state transition % -> % is not allowed', OLD.state, NEW.state
        USING ERRCODE = 'check_violation';
END;
$$;

ALTER TABLE campaigns
DROP CONSTRAINT campaigns_state_check,
ADD CONSTRAINT campaigns_state_check CHECK (
    state IN ('draft', 'audience_ready', 'content_ready', 'approved', 'sending', 'completed')
),
DROP CONSTRAINT campaigns_approval_present_check,
ADD CONSTRAINT campaigns_approval_present_check CHECK (
    (state IN ('approved', 'sending', 'completed')) = (approved_content_version_id IS NOT NULL)
);

ALTER TABLE campaigns DROP COLUMN scheduled_at;
