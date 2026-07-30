-- Admin escape hatch on top of every discover_sections row's algorithmic
-- fill: force a specific novel in ('pinned') or out ('excluded'),
-- regardless of what the section's own scope/sort/filter would produce.
-- Algorithmic stays the default; this is the exception valve, not a
-- replacement — every automatic value elsewhere in this system keeps its
-- own admin-editable field too (view_count, is_recommended,
-- is_exclusive), same governing pattern applied at the section-membership
-- layer instead of the raw-metric layer.
CREATE TABLE novel_section_overrides (
    section_key text NOT NULL REFERENCES discover_sections (key) ON DELETE CASCADE,
    novel_id    uuid NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    type        text NOT NULL CHECK (type IN ('pinned', 'excluded')),
    -- Only meaningful for 'pinned': fixed slot position (0-based) in the
    -- section; NULL pins to the front. Irrelevant for 'excluded'.
    position   integer,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (section_key, novel_id)
);

CREATE INDEX novel_section_overrides_section_index ON novel_section_overrides (section_key);
