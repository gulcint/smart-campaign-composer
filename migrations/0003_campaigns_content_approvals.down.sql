-- allow-destructive: reverts 0003.
DROP TRIGGER campaigns_state_transition_trg ON campaigns;
DROP FUNCTION campaigns_state_transition();
DROP TABLE approvals;
ALTER TABLE campaigns DROP CONSTRAINT campaigns_approved_content_version_id_fkey;
DROP TABLE content_versions;
DROP TABLE campaigns;
