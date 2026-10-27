-- Per-campaign generation lease (db-schema.md §3.4, D52): one segment/content generation at a
-- time across every API process. Written and checked under the campaign row lock; a lease whose
-- generation_expires_at has passed is free again, so a crashed process cannot block the campaign.

ALTER TABLE campaigns
ADD COLUMN generation_token uuid,
ADD COLUMN generation_expires_at timestamptz,
ADD CONSTRAINT campaigns_generation_lease_check CHECK (
    (generation_token IS NULL) = (generation_expires_at IS NULL)
);
