-- Phase 5d: a free "Support" tap, no money involved (see the plan's
-- Phase 5d note — real gifting is a separate, larger initiative once a
-- wallet system exists). Functionally a like/favorite: one per user
-- per novel, backing novels.support_count.
CREATE TABLE novel_supports (
    novel_id   uuid NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (novel_id, user_id)
);

ALTER TABLE novels ADD COLUMN support_count integer NOT NULL DEFAULT 0;
