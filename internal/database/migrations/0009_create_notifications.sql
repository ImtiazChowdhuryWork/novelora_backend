CREATE TABLE notifications (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    novel_id   uuid        NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    chapter_id uuid        NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    title      text        NOT NULL,
    body       text        NOT NULL,
    is_read    boolean     NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notifications_user_created_index ON notifications (user_id, created_at DESC);
CREATE INDEX notifications_user_unread_index  ON notifications (user_id) WHERE is_read = false;
