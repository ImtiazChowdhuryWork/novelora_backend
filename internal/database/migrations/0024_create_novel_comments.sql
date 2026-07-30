-- Phase 5c: novel-level comments (not per-chapter, matching the
-- reference screenshot). Exactly one level of nesting — a comment's
-- parent_comment_id must point to a top-level comment (parent_comment_id
-- IS NULL), enforced in NovelCommentService, not the schema, so a
-- self-referencing FK is enough.
--
-- deleted_at is a soft delete: a removed comment (self or moderation)
-- keeps its row so replies under it don't lose their parent — the API
-- blanks the body and returns is_deleted instead of dropping the row.
CREATE TABLE novel_comments (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id           uuid NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    user_id            uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    parent_comment_id  uuid REFERENCES novel_comments (id) ON DELETE CASCADE,
    body               text NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    deleted_at         timestamptz
);

CREATE INDEX novel_comments_top_level_index
    ON novel_comments (novel_id, created_at DESC)
    WHERE parent_comment_id IS NULL;
CREATE INDEX novel_comments_replies_index ON novel_comments (parent_comment_id);
