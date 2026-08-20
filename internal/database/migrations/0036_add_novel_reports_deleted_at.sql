-- Lets an admin remove a report from the Reports list (soft delete —
-- see NovelReportService.AdminDelete) without losing the audit trail
-- immediately; a background ticker hard-deletes anything soft-deleted
-- for more than 30 days (see runReportPurgeTicker in main.go).
ALTER TABLE novel_reports ADD COLUMN deleted_at timestamptz;
CREATE INDEX novel_reports_deleted_at_index ON novel_reports (deleted_at) WHERE deleted_at IS NOT NULL;
