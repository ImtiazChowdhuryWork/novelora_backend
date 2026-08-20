-- Moderation workflow v2: a proper state machine (see
-- NovelReportService's MarkUnderReview/HoldChapter/HoldNovel/
-- ApproveRelease/RejectRelease). novel_reports itself needs no new
-- columns — status now carries a wider vocabulary
-- (submitted/under_review/rejected/chapter_on_hold/novel_on_hold/
-- pending_release_review/resolved), validated in Go same as before.
--
-- moderation_actions is the per-report action history audit_logs
-- can't provide (no entity_id filter) — every state transition writes
-- one row here; the admin drawer's History section reads it back.
CREATE TABLE moderation_actions (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id   uuid NOT NULL REFERENCES novel_reports (id) ON DELETE CASCADE,
    action_type text NOT NULL,
    admin_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    notes       text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX moderation_actions_report_index ON moderation_actions (report_id, created_at DESC);

-- The author's side of a hold: an explanation (+ optional proof
-- images) asking the admin to restore chapter_on_hold/novel_on_hold
-- content. Replaces the old bare-message Resubmit. status stays
-- 'pending' until an admin approves (content restored, report ->
-- resolved) or rejects (admin_comment set, report reverts to whichever
-- hold status it came from — see RejectRelease).
CREATE TABLE release_requests (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id    uuid NOT NULL REFERENCES novel_reports (id) ON DELETE CASCADE,
    author_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    explanation  text NOT NULL,
    status       text NOT NULL DEFAULT 'pending',
    admin_comment text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    reviewed_at  timestamptz
);
CREATE INDEX release_requests_report_index ON release_requests (report_id, created_at DESC);

CREATE TABLE release_request_images (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    release_request_id uuid NOT NULL REFERENCES release_requests (id) ON DELETE CASCADE,
    image_url          text NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX release_request_images_request_index ON release_request_images (release_request_id);

-- Migrate the old, coarser status vocabulary to the new state machine.
-- action_required didn't distinguish chapter- vs. novel-level holds;
-- infer it from whether the report references a chapter, which matches
-- how every action_required row this session was actually reached
-- (the admin's Take Action button was always chapter-scoped when
-- chapter_id was present).
UPDATE novel_reports SET status = 'submitted' WHERE status = 'pending';
UPDATE novel_reports SET status = 'chapter_on_hold' WHERE status = 'action_required' AND chapter_id IS NOT NULL;
UPDATE novel_reports SET status = 'novel_on_hold' WHERE status = 'action_required' AND chapter_id IS NULL;
UPDATE novel_reports SET status = 'pending_release_review' WHERE status = 'resubmitted';
UPDATE novel_reports SET status = 'resolved' WHERE status = 'reviewed';
UPDATE novel_reports SET status = 'rejected' WHERE status = 'dismissed';
ALTER TABLE novel_reports ALTER COLUMN status SET DEFAULT 'submitted';
