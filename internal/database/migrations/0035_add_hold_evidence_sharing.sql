-- Lets an admin, at the moment of HoldChapter/HoldNovel, (a) choose
-- whether the reporter's already-submitted evidence images get shown
-- to the author (default false — not automatic; see
-- NovelReportService.HoldChapter/HoldNovel) and (b) attach their own
-- evidence images to that specific hold decision, kept separate from
-- the reporter's (moderation_action_images, not novel_report_images).
ALTER TABLE novel_reports ADD COLUMN share_reporter_evidence boolean NOT NULL DEFAULT false;

CREATE TABLE moderation_action_images (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    moderation_action_id  uuid NOT NULL REFERENCES moderation_actions (id) ON DELETE CASCADE,
    image_url             text NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX moderation_action_images_action_index ON moderation_action_images (moderation_action_id);
