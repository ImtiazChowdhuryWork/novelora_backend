ALTER TABLE users ADD COLUMN google_id text;

CREATE UNIQUE INDEX users_google_id_unique
    ON users (google_id)
    WHERE google_id IS NOT NULL;
