-- allow-destructive: reverts 0007.
ALTER TABLE campaigns
DROP CONSTRAINT campaigns_generation_lease_check,
DROP COLUMN generation_expires_at,
DROP COLUMN generation_token;
