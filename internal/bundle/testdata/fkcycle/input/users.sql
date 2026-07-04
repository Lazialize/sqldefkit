-- References orders back, closing the cycle with orders.sql.
CREATE TABLE users (
    id serial PRIMARY KEY,
    email text NOT NULL UNIQUE,
    favorite_order_id integer REFERENCES orders (id)
);
