-- Sorts before products.sql and users.sql lexicographically, but
-- references both, so it must be reordered after them in output.
CREATE TABLE orders (
    id serial PRIMARY KEY,
    user_id integer NOT NULL REFERENCES users(id),
    product_id integer NOT NULL REFERENCES products(id)
);

CREATE INDEX orders_user_id_idx ON orders (user_id);
