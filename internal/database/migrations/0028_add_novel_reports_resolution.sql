-- Admin's typed note when actioning a report (reviewed/dismissed) —
-- shown back to the reporting reader on the app's "My Reports" page
-- and mirrored into their inbox notification body. Empty until acted
-- on; there's no author-account system yet, so this is a plain
-- admin-typed note, not a structured resolution against an author.
ALTER TABLE novel_reports ADD COLUMN resolution_note text NOT NULL DEFAULT '';
