-- Per-order totals, with the placing user's email pulled in via a scalar
-- subquery. The view body's FROM/JOIN targets (orders, order_items,
-- products) are auto-detected, but "users" only appears inside a
-- parenthesized subquery in the SELECT list, which the best-effort
-- FROM/JOIN scanner intentionally does not descend into -- so the
-- dependency on users must be declared explicitly.
-- sqldefkit:require users
CREATE VIEW order_totals AS
SELECT
    o.id AS order_id,
    (SELECT email FROM users u WHERE u.id = o.user_id) AS user_email,
    count(oi.id) AS item_count,
    sum(oi.quantity * p.price_cents) AS total_cents
FROM orders o
JOIN order_items oi ON oi.order_id = o.id
JOIN products p ON p.id = oi.product_id
GROUP BY o.id, o.user_id;
