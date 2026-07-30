-- One row per (user, novel): where a logged-in reader last left off.
-- Upserted on every chapter open — see ReadingHistoryService.RecordRead.
-- last_chapter_id is nullable/SET NULL on delete so a since-removed
-- chapter doesn't wipe out the novel's place in the reader's history.
CREATE TABLE reading_history (
    user_id         uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    novel_id        uuid NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    last_chapter_id uuid REFERENCES chapters (id) ON DELETE SET NULL,
    last_read_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, novel_id)
);

CREATE INDEX reading_history_user_last_read_index ON reading_history (user_id, last_read_at DESC);
