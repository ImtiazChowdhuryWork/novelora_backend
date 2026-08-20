-- Snapshots the reason's parent type at submit time — same rationale
-- as `reason` itself (migration 0027): free text, not a foreign key,
-- so it survives the type being renamed or deleted later. Powers the
-- author dashboard's "i" info-capsule on a report's type pill.
ALTER TABLE novel_reports ADD COLUMN reason_type text NOT NULL DEFAULT '';
ALTER TABLE novel_reports ADD COLUMN reason_type_description text NOT NULL DEFAULT '';
