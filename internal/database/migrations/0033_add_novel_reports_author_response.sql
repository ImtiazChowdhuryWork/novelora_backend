-- Moderation workflow: admin's "action required" note lives in the
-- existing resolution_note column; this is the author's reply when
-- they fix the issue and request re-review (see NovelReportService's
-- Resubmit). Reset to '' whenever an admin issues a new note, so a
-- stale reply from a previous round-trip never lingers next to it.
ALTER TABLE novel_reports ADD COLUMN author_response text NOT NULL DEFAULT '';
