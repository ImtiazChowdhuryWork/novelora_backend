-- Tracks which novels are currently in each tracked ranked/curated
-- Discover section (Trending, a category's own Most Read, ...), so a
-- background job can diff the current top-N against this and notify
-- readers only about novels newly entering, never ones already there.
CREATE TABLE novel_section_memberships (
    novel_id    uuid        NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    section_key text        NOT NULL,
    entered_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (novel_id, section_key)
);

CREATE INDEX novel_section_memberships_section_index ON novel_section_memberships (section_key);
