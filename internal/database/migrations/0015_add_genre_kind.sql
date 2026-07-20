ALTER TABLE genres ADD COLUMN kind text NOT NULL DEFAULT 'genre'
    CHECK (kind IN ('genre', 'tag'));
