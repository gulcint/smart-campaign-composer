-- Single-row record of the loaded seed dataset (db-schema.md §3.8, seed.md §2).

CREATE TABLE seed_runs (
    id integer NOT NULL,
    seed bigint NOT NULL,
    users integer NOT NULL, -- noqa: RF04
    ref_time timestamptz NOT NULL,
    dataset_sha256 text NOT NULL,
    loaded_at timestamptz NOT NULL,
    CONSTRAINT seed_runs_pkey PRIMARY KEY (id),
    CONSTRAINT seed_runs_single_row_check CHECK (id = 1)
);
