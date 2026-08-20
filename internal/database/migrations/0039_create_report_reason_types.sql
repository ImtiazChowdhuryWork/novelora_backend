-- Reason Types group the flat report_reasons list into broader
-- categories — what the author dashboard shows on a report instead of
-- (or alongside) the reader's specific reason, with `description` as
-- the "what does this mean" info-tap copy. Same free-text/no-history-
-- coupling philosophy as report_reasons itself (see migration 0037):
-- novel_reports snapshots a type's label+description at submit time
-- (migration 0041), so renaming/deleting a type later never rewrites
-- a report already sent.
CREATE TABLE report_reason_types (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    label       text NOT NULL UNIQUE,
    description text NOT NULL DEFAULT '',
    position    integer NOT NULL DEFAULT 0,
    created_at  timestamptz NOT NULL DEFAULT now()
);

INSERT INTO report_reason_types (label, description, position) VALUES
    ('Content Policy', 'The content may violate community guidelines — spam, harassment, or inappropriate material.', 0),
    ('Copyright', 'The content may infringe someone else''s copyright or original work.', 1),
    ('Technical Issue', 'Something in the content is broken — missing chapters, garbled text, or similar.', 2),
    ('Other', 'Doesn''t fit another category — the reporter explained it in their own words.', 3);
