-- allow-destructive: reverts 0009; English versions and audience language splits are lost (db-schema.md §3.4, §3.5).

ALTER TABLE campaigns
DROP CONSTRAINT campaigns_audience_languages_check,
DROP COLUMN audience_languages;

ALTER TABLE content_versions
DROP CONSTRAINT content_versions_worst_case_en_check,
DROP CONSTRAINT content_versions_en_all_or_none_check,
DROP COLUMN worst_case_en_subject_runes,
DROP COLUMN worst_case_en_sms_runes,
DROP COLUMN en_email_body,
DROP COLUMN en_email_subject,
DROP COLUMN en_sms_body;
