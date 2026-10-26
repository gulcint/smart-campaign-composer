#!/usr/bin/env bash
# Runs once, as the Postgres superuser, when the data directory is initialised
# (docker-entrypoint-initdb.d). Creates the two roles of db-schema.md §1:
#   scc_migrator - owns the databases and every schema object, runs DDL (cmd/migrate)
#   scc_app      - DML only: no CREATE on public, owns nothing (REQ-SEC-02)
# and one database per name in SCC_DATABASES (default "scc").
# Roles and databases are not migrations: migrations run as scc_migrator, which
# cannot create roles.
set -euo pipefail

: "${SCC_MIGRATOR_PASSWORD:?SCC_MIGRATOR_PASSWORD is required}"
: "${SCC_APP_PASSWORD:?SCC_APP_PASSWORD is required}"
databases="${SCC_DATABASES:-scc}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v migrator_pw="$SCC_MIGRATOR_PASSWORD" -v app_pw="$SCC_APP_PASSWORD" <<'SQL'
CREATE ROLE scc_migrator LOGIN PASSWORD :'migrator_pw';
CREATE ROLE scc_app LOGIN PASSWORD :'app_pw';
SQL

for db in $databases; do
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
    -v db="$db" <<'SQL'
CREATE DATABASE :"db" OWNER scc_migrator;
REVOKE ALL ON DATABASE :"db" FROM PUBLIC;
GRANT CONNECT, TEMPORARY ON DATABASE :"db" TO scc_app;
SQL
  # In PostgreSQL 16 the public schema belongs to pg_database_owner, i.e. scc_migrator.
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$db" <<'SQL'
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO scc_app;
ALTER DEFAULT PRIVILEGES FOR ROLE scc_migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO scc_app;
ALTER DEFAULT PRIVILEGES FOR ROLE scc_migrator IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO scc_app;
ALTER DEFAULT PRIVILEGES FOR ROLE scc_migrator IN SCHEMA public
  GRANT EXECUTE ON FUNCTIONS TO scc_app;
SQL
done
