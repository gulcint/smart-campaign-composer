-- Campaigns, immutable content versions and approvals (db-schema.md §3.4–§3.6).

CREATE TABLE campaigns (
    id uuid NOT NULL,
    brief text NOT NULL,
    state text NOT NULL,
    segment_rule jsonb,
    segment_reference_time timestamptz,
    segment_revision integer NOT NULL DEFAULT 0,
    segment_llm_attempts integer,
    segment_generated_at timestamptz,
    audience_matched integer,
    audience_sms integer,
    audience_email integer,
    longest_first_name integer,
    longest_last_name integer,
    longest_city integer,
    approved_content_version_id uuid,
    sending_started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT campaigns_pkey PRIMARY KEY (id),
    CONSTRAINT campaigns_brief_length_check CHECK (char_length(brief) BETWEEN 1 AND 2000),
    CONSTRAINT campaigns_state_check CHECK (
        state IN ('draft', 'audience_ready', 'content_ready', 'approved', 'sending', 'completed')
    ),
    CONSTRAINT campaigns_segment_revision_check CHECK (segment_revision >= 0),
    CONSTRAINT campaigns_segment_llm_attempts_check CHECK (segment_llm_attempts BETWEEN 1 AND 2),
    CONSTRAINT campaigns_audience_check CHECK (
        audience_matched >= 0 AND audience_sms >= 0 AND audience_email >= 0
    ),
    CONSTRAINT campaigns_longest_check CHECK (
        longest_first_name >= 0 AND longest_last_name >= 0 AND longest_city >= 0
    ),
    CONSTRAINT campaigns_segment_present_check CHECK (
        state = 'draft' OR (segment_rule IS NOT NULL AND segment_reference_time IS NOT NULL)
    ),
    CONSTRAINT campaigns_approval_present_check CHECK (
        (state IN ('approved', 'sending', 'completed')) = (approved_content_version_id IS NOT NULL)
    ),
    CONSTRAINT campaigns_sending_started_check CHECK (
        (state IN ('sending', 'completed')) = (sending_started_at IS NOT NULL)
    ),
    CONSTRAINT campaigns_completed_check CHECK (
        (state = 'completed') = (completed_at IS NOT NULL)
    )
);

CREATE TABLE content_versions (
    id uuid NOT NULL,
    campaign_id uuid NOT NULL,
    version_number integer NOT NULL,
    segment_revision integer NOT NULL,
    status text NOT NULL,
    source text NOT NULL, -- noqa: RF04
    sms_body text NOT NULL,
    email_subject text NOT NULL,
    email_body text NOT NULL,
    validation_errors jsonb NOT NULL DEFAULT '[]'::jsonb,
    llm_attempts integer,
    worst_case_sms_runes integer,
    worst_case_subject_runes integer,
    created_at timestamptz NOT NULL,
    CONSTRAINT content_versions_pkey PRIMARY KEY (id),
    CONSTRAINT content_versions_campaign_id_fkey FOREIGN KEY (campaign_id)
    REFERENCES campaigns (id) ON DELETE CASCADE,
    CONSTRAINT content_versions_campaign_version_key UNIQUE (campaign_id, version_number),
    CONSTRAINT content_versions_id_status_key UNIQUE (id, status),
    CONSTRAINT content_versions_version_number_check CHECK (version_number >= 1),
    CONSTRAINT content_versions_status_check CHECK (status IN ('validated', 'invalid')),
    CONSTRAINT content_versions_source_check CHECK (source IN ('llm', 'edit')),
    CONSTRAINT content_versions_errors_check CHECK (
        (status = 'validated') = (validation_errors = '[]'::jsonb)
    ),
    CONSTRAINT content_versions_llm_attempts_check CHECK (
        (source = 'edit' AND llm_attempts IS NULL)
        OR (source = 'llm' AND llm_attempts BETWEEN 1 AND 2)
    )
);

-- Deferred so that seed --reset can delete content versions and campaigns in one transaction.
ALTER TABLE campaigns
ADD CONSTRAINT campaigns_approved_content_version_id_fkey
FOREIGN KEY (approved_content_version_id) REFERENCES content_versions (id)
DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE approvals (
    id uuid NOT NULL,
    campaign_id uuid NOT NULL,
    content_version_id uuid NOT NULL,
    content_status text NOT NULL DEFAULT 'validated',
    approved_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoke_reason text,
    CONSTRAINT approvals_pkey PRIMARY KEY (id),
    CONSTRAINT approvals_campaign_id_fkey FOREIGN KEY (campaign_id)
    REFERENCES campaigns (id) ON DELETE CASCADE,
    CONSTRAINT approvals_content_version_fk FOREIGN KEY (content_version_id, content_status)
    REFERENCES content_versions (id, status),
    CONSTRAINT approvals_content_status_check CHECK (content_status = 'validated'),
    CONSTRAINT approvals_revoke_reason_check CHECK (
        revoke_reason IN ('content_changed', 'segment_changed')
    ),
    CONSTRAINT approvals_revoked_check CHECK ((revoked_at IS NULL) = (revoke_reason IS NULL))
);

CREATE UNIQUE INDEX approvals_one_active_idx ON approvals (campaign_id) WHERE revoked_at IS NULL;

-- Rejects state changes outside the campaign state machine with SQLSTATE 23514.
CREATE FUNCTION campaigns_state_transition() RETURNS trigger
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

CREATE TRIGGER campaigns_state_transition_trg
BEFORE UPDATE OF state ON campaigns
FOR EACH ROW EXECUTE FUNCTION campaigns_state_transition();
