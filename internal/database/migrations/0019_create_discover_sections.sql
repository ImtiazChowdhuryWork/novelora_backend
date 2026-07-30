-- Single source of truth for "which genres/rules populate which Discover
-- section" — replaces the two hand-duplicated lists this used to live in
-- (DiscoverCubit's Dart constants and RankingNotificationService's
-- defaultRankingSections), which had a self-documented drift risk: a
-- genre renamed in the admin Genres page could silently stop matching in
-- one list and not the other. Typed filter columns rather than a single
-- jsonb blob — simpler to query/seed and matches NovelListFilter's own
-- shape (Status/IsRecommended/IsExclusive), which every row here
-- ultimately compiles down to.
CREATE TABLE discover_sections (
    key      text PRIMARY KEY,
    category text NOT NULL,
    label    text NOT NULL,

    -- 'ranked_grid' (RankedGridShelf), 'shelf' (NovelShelf), 'picks_feed'
    -- (NovelPicksFeed) — the three generic presentational widgets every
    -- section already reuses (see novelora_app README §1.7).
    layout text NOT NULL CHECK (layout IN ('ranked_grid', 'shelf', 'picks_feed')),

    -- 'genre' (single genre_names entry), 'global' (genre_names empty),
    -- 'multi_genre' (genre_names merged/deduped client-side, since the
    -- catalog API only supports one genre_id per request).
    scope       text NOT NULL CHECK (scope IN ('genre', 'global', 'multi_genre')),
    genre_names text[] NOT NULL DEFAULT '{}',

    sort               text NOT NULL DEFAULT '' CHECK (sort IN ('', 'trending', 'views', 'rating', 'new')),
    status_filter      text NOT NULL DEFAULT '' CHECK (status_filter IN ('', 'ongoing', 'completed')),
    recommended_filter boolean, -- NULL = no filter
    exclusive_filter   boolean, -- NULL = no filter

    -- Other section keys whose novel ids this section excludes — the
    -- existing small-catalog overlap-avoidance pattern (e.g. Romance's
    -- "Editor's Picks" excluding "Love On Top"), formalized instead of
    -- hand-wired per section in Dart.
    exclude_section_keys text[] NOT NULL DEFAULT '{}',

    position   integer     NOT NULL DEFAULT 0,
    active     boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX discover_sections_category_index ON discover_sections (category, position);
