-- An order placed by a user. Sorts lexicographically before users.sql,
-- but depends on it via the REFERENCES clause below, so sqldefkit must
-- reorder it after users.sql in the bundled output.
CREATE TABLE orders (
    id serial PRIMARY KEY,
    user_id integer NOT NULL REFERENCES users(id),
    placed_at timestamptz NOT NULL DEFAULT now()
);
