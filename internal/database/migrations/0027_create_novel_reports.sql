-- Reader-submitted "Report this novel" (Book Detail's flag icon).
-- reason is a fixed short enum, validated in NovelReportService, not
-- a DB CHECK — same choice as novel_comments' body-length validation,
-- keeps the allowed set changeable without a migration.
--
-- status is the moderation workflow: pending -> reviewed | dismissed.
-- reviewed_by/reviewed_at are set together when an admin actions a
-- report (see NovelReportRepository.UpdateStatus); both stay NULL
-- while pending.
CREATE TABLE novel_reports (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id     uuid NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    reason       text NOT NULL,
    details      text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT 'pending',
    reviewed_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    reviewed_at  timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX novel_reports_status_index ON novel_reports (status, created_at DESC);
CREATE INDEX novel_reports_novel_index ON novel_reports (novel_id);
