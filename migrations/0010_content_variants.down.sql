-- allow-destructive: reverts 0010; variant B texts, angles and delivery variants are lost (db-schema.md §3.5.1).

ALTER TABLE deliveries
DROP CONSTRAINT deliveries_variant_check,
DROP COLUMN variant;

ALTER TABLE content_versions
DROP CONSTRAINT content_versions_worst_case_b_check,
DROP CONSTRAINT content_versions_b_en_check,
DROP CONSTRAINT content_versions_b_all_or_none_check,
DROP COLUMN worst_case_b_en_subject_runes,
DROP COLUMN worst_case_b_en_sms_runes,
DROP COLUMN worst_case_b_subject_runes,
DROP COLUMN worst_case_b_sms_runes,
DROP COLUMN b_en_email_body,
DROP COLUMN b_en_email_subject,
DROP COLUMN b_en_sms_body,
DROP COLUMN angle_b,
DROP COLUMN angle_a,
DROP COLUMN b_email_body,
DROP COLUMN b_email_subject,
DROP COLUMN b_sms_body;
