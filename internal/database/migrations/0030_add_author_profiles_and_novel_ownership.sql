-- Author accounts, Phase 1: "author" is an additive capability on an
-- existing user account (a reader can also be an author), not a role
-- swap — see the migration comment in 0027 for why novels.author_name
-- stayed a free-text string until now. owner_user_id lets an
-- author-uploaded novel be scoped to its account while every existing
-- admin-uploaded novel (owner_user_id NULL) keeps working unchanged
-- via author_name, which stays the reader-facing display field either way.

CREATE TABLE author_profiles (
    user_id    uuid PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    pen_name   text NOT NULL,
    bio        text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE novels ADD COLUMN owner_user_id uuid REFERENCES users (id) ON DELETE SET NULL;
CREATE INDEX novels_owner_index ON novels (owner_user_id) WHERE deleted_at IS NULL;
