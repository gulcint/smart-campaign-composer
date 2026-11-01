-- A/B variants (P9, D64; db-schema.md §3.5, §3.5.1, §3.7): the optional variant B of a content
-- version, the angle labels and the variant each delivery received. 0009 is not edited.

ALTER TABLE content_versions
ADD COLUMN b_sms_body text,
ADD COLUMN b_email_subject text,
ADD COLUMN b_email_body text,
ADD COLUMN angle_a text,
ADD COLUMN angle_b text,
ADD COLUMN b_en_sms_body text,
ADD COLUMN b_en_email_subject text,
ADD COLUMN b_en_email_body text,
ADD COLUMN worst_case_b_sms_runes integer,
ADD COLUMN worst_case_b_subject_runes integer,
ADD COLUMN worst_case_b_en_sms_runes integer,
ADD COLUMN worst_case_b_en_subject_runes integer,
-- No half variant B: its three texts and both angles, or none (empty strings allowed for an invalid draft).
ADD CONSTRAINT content_versions_b_all_or_none_check CHECK (
    (
        b_sms_body IS NULL AND b_email_subject IS NULL AND b_email_body IS NULL
        AND angle_a IS NULL AND angle_b IS NULL
    )
    OR (
        b_sms_body IS NOT NULL AND b_email_subject IS NOT NULL AND b_email_body IS NOT NULL
        AND angle_a IS NOT NULL AND angle_b IS NOT NULL
    )
),
-- B's English version: all three or none, and present iff both variant B and A's English exist.
ADD CONSTRAINT content_versions_b_en_check CHECK (
    (
        (b_en_sms_body IS NULL AND b_en_email_subject IS NULL AND b_en_email_body IS NULL)
        OR (b_en_sms_body IS NOT NULL AND b_en_email_subject IS NOT NULL AND b_en_email_body IS NOT NULL)
    )
    AND (b_en_sms_body IS NOT NULL) = (b_sms_body IS NOT NULL AND en_sms_body IS NOT NULL)
),
-- No variant B worst case without the matching texts.
ADD CONSTRAINT content_versions_worst_case_b_check CHECK (
    (b_sms_body IS NOT NULL OR (worst_case_b_sms_runes IS NULL AND worst_case_b_subject_runes IS NULL))
    AND (
        b_en_sms_body IS NOT NULL
        OR (worst_case_b_en_sms_runes IS NULL AND worst_case_b_en_subject_runes IS NULL)
    )
);

-- The variant a delivery received; existing rows and non-A/B sends keep the default.
ALTER TABLE deliveries
ADD COLUMN variant text NOT NULL DEFAULT 'a',
ADD CONSTRAINT deliveries_variant_check CHECK (variant IN ('a', 'b'));
