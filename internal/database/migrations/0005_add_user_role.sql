ALTER TABLE users ADD COLUMN role text NOT NULL DEFAULT 'reader';

ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('reader', 'admin'));
