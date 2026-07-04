-- Mutually references users, forming a dependency cycle sqldefkit
-- splits automatically by moving the foreign key to an ALTER TABLE.
CREATE TABLE orders (
    id serial PRIMARY KEY,
    user_id integer NOT NULL REFERENCES users (id)
);
