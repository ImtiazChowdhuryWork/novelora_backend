-- Author-moderation panel, real-accounts phase: author_strikes was
-- created (migration 0029) keyed only by the free-text author_name,
-- since no account system existed yet. Now that novels can carry a
-- real owner_user_id (migration 0030), strikes/bulk-hide against an
-- author-dashboard-created novel should key off the real account
-- instead — nullable and additive, so admin-uploaded/unclaimed novels
-- (owner_user_id NULL) keep working exactly as before via author_name.
-- Existing strike rows have no way to backfill a user id (never
-- captured one) and stay name-only.

ALTER TABLE author_strikes ADD COLUMN owner_user_id uuid REFERENCES users (id) ON DELETE SET NULL;
CREATE INDEX author_strikes_owner_index ON author_strikes (owner_user_id) WHERE owner_user_id IS NOT NULL;
