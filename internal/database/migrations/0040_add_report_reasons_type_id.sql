-- Assigns every report_reasons row to a report_reason_types row.
ALTER TABLE report_reasons ADD COLUMN type_id uuid REFERENCES report_reason_types (id);

UPDATE report_reasons SET type_id = (SELECT id FROM report_reason_types WHERE label = 'Content Policy')
WHERE label IN ('Spam or advertising', 'Inappropriate or offensive content', 'Harassment or hate speech');

UPDATE report_reasons SET type_id = (SELECT id FROM report_reason_types WHERE label = 'Copyright')
WHERE label = 'Plagiarism or copyright issue';

UPDATE report_reasons SET type_id = (SELECT id FROM report_reason_types WHERE label = 'Technical Issue')
WHERE label = 'Broken content (missing chapters, garbled text)';

UPDATE report_reasons SET type_id = (SELECT id FROM report_reason_types WHERE label = 'Other')
WHERE label = 'Something else';

-- Any row this migration didn't already recognize by label (a custom
-- reason an admin already added before this shipped) falls back to
-- "Other" rather than being left NULL.
UPDATE report_reasons SET type_id = (SELECT id FROM report_reason_types WHERE label = 'Other')
WHERE type_id IS NULL;

ALTER TABLE report_reasons ALTER COLUMN type_id SET NOT NULL;
