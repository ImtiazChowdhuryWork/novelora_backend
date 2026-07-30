-- Phase 5b: real per-user star ratings. rating is a 1-5 star vote;
-- novels.average_rating is stored on the *admin's existing 0-10 scale*
-- (rating * 2, rounded to one decimal) so the two numbers are directly
-- comparable — average_rating is the real signal once a novel has
-- enough votes to trust, average_rating falls back to the admin-typed
-- novels.rating below that threshold (see NovelRepository.List's
-- "rating" sort case).
CREATE TABLE novel_ratings (
    novel_id   uuid NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    rating     smallint NOT NULL CHECK (rating BETWEEN 1 AND 5),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (novel_id, user_id)
);

ALTER TABLE novels
    ADD COLUMN average_rating numeric(3, 1),
    ADD COLUMN rating_count   integer NOT NULL DEFAULT 0;
