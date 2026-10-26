-- Restores the grant given by the default privileges of the roles init script.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'scc_app') THEN
        GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE schema_migrations TO scc_app;
    END IF;
END;
$$;
