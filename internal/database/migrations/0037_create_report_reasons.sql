-- Admin-manageable report reason types (Book Detail's flag-icon
-- "Report this novel" sheet). novel_reports.reason stores the label
-- text itself, not a foreign key to this table — see 0027's comment:
-- that column was already free text specifically so the allowed set
-- could change without touching history. This table is purely
-- "today's menu of choices" offered to the reporter and managed from
-- the admin panel; deleting or renaming a row here never affects a
-- report already submitted with the old label.
CREATE TABLE report_reasons (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    label            text NOT NULL UNIQUE,
    requires_details boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- Seed with the reasons the app already hardcoded, in the same order,
-- so rollout doesn't change what readers see. "Something else" is the
-- only one that ever required free-text details.
INSERT INTO report_reasons (label, requires_details) VALUES
    ('Spam or advertising', false),
    ('Plagiarism or copyright issue', false),
    ('Inappropriate or offensive content', false),
    ('Harassment or hate speech', false),
    ('Broken content (missing chapters, garbled text)', false),
    ('Something else', true);
