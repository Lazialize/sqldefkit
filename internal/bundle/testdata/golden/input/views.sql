-- users is only reachable here via a parenthesized subquery, which the
-- best-effort FROM/JOIN scanner does not descend into, so it must be
-- declared explicitly.
-- sqldefkit:require users
CREATE VIEW order_summary AS
SELECT
    o.id AS order_id,
    (SELECT email FROM users u WHERE u.id = o.user_id) AS user_email,
    p.name AS product_name
FROM orders o
JOIN products p ON p.id = o.product_id;
