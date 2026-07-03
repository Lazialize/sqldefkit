-- Sellable products.
CREATE TABLE products (
    id serial PRIMARY KEY,
    name text NOT NULL,
    price_cents integer NOT NULL
);
