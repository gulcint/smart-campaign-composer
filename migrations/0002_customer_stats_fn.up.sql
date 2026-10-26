-- customer_stats_at(ref_time): the only source of the segment DSL's statistics fields
-- (db-schema.md §3.3). The reference time is a parameter; now() is never used.

CREATE FUNCTION customer_stats_at(ref_time timestamptz)
RETURNS TABLE (
    customer_id uuid,
    last_cart_add_at timestamptz,
    last_purchase_at timestamptz,
    orders_30d integer,
    cart_adds_30d integer,
    total_orders integer,
    total_spent numeric(14, 2)
)
LANGUAGE sql
STABLE
AS $$
    SELECT
        c.id AS customer_id,
        max(e.occurred_at) FILTER (WHERE e.type = 'cart_add') AS last_cart_add_at,
        max(e.occurred_at) FILTER (WHERE e.type = 'purchase') AS last_purchase_at,
        (count(*) FILTER (
            WHERE e.type = 'purchase' AND e.occurred_at >= ref_time - interval '30 days'
        ))::integer AS orders_30d,
        (count(*) FILTER (
            WHERE e.type = 'cart_add' AND e.occurred_at >= ref_time - interval '30 days'
        ))::integer AS cart_adds_30d,
        (count(*) FILTER (WHERE e.type = 'purchase'))::integer AS total_orders,
        coalesce(sum(e.amount) FILTER (WHERE e.type = 'purchase'), 0)::numeric(14, 2) AS total_spent
    FROM customers AS c
    LEFT JOIN events AS e
        ON e.customer_id = c.id AND e.occurred_at <= ref_time
    GROUP BY c.id
$$;
