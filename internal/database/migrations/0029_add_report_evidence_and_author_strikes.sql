-- Reader-attached evidence for a report (Book Detail's flag icon,
-- extended 2026-08-14): which chapter (optional — NULL means "the
-- whole novel") and up to 3 screenshot images (see
-- NovelReportService.maxReportImages).
ALTER TABLE novel_reports ADD COLUMN chapter_id uuid REFERENCES chapters (id) ON DELETE SET NULL;

CREATE TABLE novel_report_images (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id  uuid NOT NULL REFERENCES novel_reports (id) ON DELETE CASCADE,
    image_url  text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX novel_report_images_report_index ON novel_report_images (report_id);

-- Author moderation (2026-08-14) — there's no author-account system
-- yet (novels.author_name is a free-text field, not a foreign key), so
-- a strike is keyed by that same string. It's a persistent admin note
-- visible across every novel by that author name; it never changes
-- content by itself — see the separate bulk-hide admin action
-- (NovelRepository.BulkHideByAuthorName) for that.
CREATE TABLE author_strikes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    author_name text NOT NULL,
    note        text NOT NULL,
    created_by  uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX author_strikes_author_index ON author_strikes (author_name);
