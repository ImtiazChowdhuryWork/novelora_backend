ALTER TABLE chapters ADD COLUMN scheduled_at timestamptz;

CREATE INDEX chapters_scheduled_at_index ON chapters (scheduled_at) WHERE scheduled_at IS NOT NULL;
