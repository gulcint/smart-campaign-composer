-- Language-based content (P8, D63; db-schema.md §3.4, §3.5): the optional English version of a
-- content version and the per-channel language split of the campaign audience.
-- 0003 is not edited (migration immutability).

ALTER TABLE content_versions
ADD COLUMN en_sms_body text,
ADD COLUMN en_email_subject text,
ADD COLUMN en_email_body text,
ADD COLUMN worst_case_en_sms_runes integer,
ADD COLUMN worst_case_en_subject_runes integer,
-- No half English version: all three texts or none (empty strings allowed for an invalid draft).
ADD CONSTRAINT content_versions_en_all_or_none_check CHECK (
    (en_sms_body IS NULL AND en_email_subject IS NULL AND en_email_body IS NULL)
    OR (en_sms_body IS NOT NULL AND en_email_subject IS NOT NULL AND en_email_body IS NOT NULL)
),
-- No English worst case without an English version.
ADD CONSTRAINT content_versions_worst_case_en_check CHECK (
    en_sms_body IS NOT NULL
    OR (worst_case_en_sms_runes IS NULL AND worst_case_en_subject_runes IS NULL)
);

-- {"sms":{"tr":n,"en":m},"email":{"tr":n,"en":m}}; NULL = segment generated before P8.
ALTER TABLE campaigns
ADD COLUMN audience_languages jsonb,
ADD CONSTRAINT campaigns_audience_languages_check CHECK (
    audience_languages IS NULL
    OR (audience_matched IS NOT NULL AND jsonb_typeof(audience_languages) = 'object')
);
