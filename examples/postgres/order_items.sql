-- Line items belonging to an order. Sorts first lexicographically among
-- all files here, yet depends on both orders.sql and products.sql, which
-- sort after it -- another case sqldefkit's dependency-based ordering
-- handles automatically.
CREATE TABLE order_items (
    id serial PRIMARY KEY,
    order_id integer NOT NULL REFERENCES orders(id),
    product_id integer NOT NULL REFERENCES products(id),
    quantity integer NOT NULL DEFAULT 1
);

CREATE INDEX order_items_order_id_idx ON order_items (order_id);
