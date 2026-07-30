-- Rolling per-day view counts, the minimum needed to compute a 7-day
-- trending window (recent activity) without a full analytics pipeline.
-- novels.view_count stays the lifetime total; this table is the extra
-- signal trending needs on top of it.
CREATE TABLE novel_daily_views (
    novel_id uuid NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    day      date NOT NULL,
    views    integer NOT NULL DEFAULT 0,
    PRIMARY KEY (novel_id, day)
);

CREATE INDEX novel_daily_views_day_index ON novel_daily_views (day);
