-- Customers and their behaviour events (db-schema.md §3.1, §3.2).
-- Only fictional contact data can be stored (REQ-SEED-02).

CREATE TABLE customers (
    id uuid NOT NULL,
    first_name text NOT NULL,
    last_name text NOT NULL,
    email text,
    phone text,
    city text NOT NULL,
    language text NOT NULL, -- noqa: RF04
    loyalty_tier text NOT NULL,
    sms_opt_in boolean NOT NULL,
    sms_opt_in_at timestamptz,
    email_opt_in boolean NOT NULL,
    email_opt_in_at timestamptz,
    created_at timestamptz NOT NULL,
    last_seen_at timestamptz,
    CONSTRAINT customers_pkey PRIMARY KEY (id),
    CONSTRAINT customers_first_name_length_check CHECK (char_length(first_name) <= 100),
    CONSTRAINT customers_last_name_length_check CHECK (char_length(last_name) <= 100),
    CONSTRAINT customers_email_fictional_check CHECK (
        email IS NULL OR email ~ '^[a-z0-9._+-]+@example\.(com|test)$'
    ),
    CONSTRAINT customers_phone_fictional_check CHECK (
        phone IS NULL OR phone ~ '^\+90500000[0-9]{4}$'
    ),
    CONSTRAINT customers_language_check CHECK (language IN ('tr', 'en')),
    CONSTRAINT customers_loyalty_tier_check CHECK (
        loyalty_tier IN ('bronze', 'silver', 'gold', 'platinum')
    ),
    CONSTRAINT customers_sms_opt_in_at_check CHECK (sms_opt_in OR sms_opt_in_at IS NULL),
    CONSTRAINT customers_email_opt_in_at_check CHECK (email_opt_in OR email_opt_in_at IS NULL)
);

CREATE TABLE events (
    id uuid NOT NULL,
    customer_id uuid NOT NULL,
    type text NOT NULL, -- noqa: RF04
    occurred_at timestamptz NOT NULL,
    amount numeric(12, 2),
    product_category text,
    CONSTRAINT events_pkey PRIMARY KEY (id),
    CONSTRAINT events_customer_id_fkey FOREIGN KEY (customer_id)
    REFERENCES customers (id) ON DELETE CASCADE,
    CONSTRAINT events_type_check CHECK (type IN ('cart_add', 'purchase', 'page_view')),
    CONSTRAINT events_amount_check CHECK (
        (type = 'purchase' AND amount IS NOT NULL AND amount > 0)
        OR (type <> 'purchase' AND amount IS NULL)
    )
);

CREATE INDEX events_customer_type_time_idx ON events (customer_id, type, occurred_at DESC);
