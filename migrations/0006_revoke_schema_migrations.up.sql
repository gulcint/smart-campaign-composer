-- The migration bookkeeping table is the migrator's business only: the default privileges of
-- the roles init script also granted it to scc_app (REQ-SEC-02). Guarded so that plain
-- migrate runs against a database without the scc_app role still work.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'scc_app') THEN
        REVOKE ALL ON TABLE schema_migrations FROM scc_app;
    END IF;
END;
$$;
