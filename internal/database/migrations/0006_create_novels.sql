CREATE TABLE novels (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    title          varchar(255) NOT NULL,
    author_name    varchar(255) NOT NULL,
    synopsis       text         NOT NULL DEFAULT '',
    cover_url      text,
    status         text         NOT NULL DEFAULT 'ongoing'
                   CHECK (status IN ('ongoing', 'completed')),
    is_short       boolean      NOT NULL DEFAULT false,
    is_recommended boolean      NOT NULL DEFAULT false,
    rating         numeric(3,1),
    view_count     bigint       NOT NULL DEFAULT 0,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    updated_at     timestamptz  NOT NULL DEFAULT now(),
    deleted_at     timestamptz
);

CREATE INDEX novels_status_index      ON novels (status)         WHERE deleted_at IS NULL;
CREATE INDEX novels_recommended_index ON novels (is_recommended) WHERE deleted_at IS NULL;

CREATE TABLE genres (
    id   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name varchar(80) NOT NULL UNIQUE
);

CREATE TABLE novel_genres (
    novel_id uuid NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    genre_id uuid NOT NULL REFERENCES genres (id) ON DELETE CASCADE,
    PRIMARY KEY (novel_id, genre_id)
);

CREATE TABLE chapters (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    novel_id     uuid        NOT NULL REFERENCES novels (id) ON DELETE CASCADE,
    number       integer     NOT NULL,
    title        varchar(255) NOT NULL DEFAULT '',
    content_json jsonb       NOT NULL DEFAULT '{}'::jsonb,
    content_text text        NOT NULL DEFAULT '',
    word_count   integer     NOT NULL DEFAULT 0,
    status       text        NOT NULL DEFAULT 'draft'
                 CHECK (status IN ('draft', 'published')),
    published_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (novel_id, number)
);

CREATE INDEX chapters_novel_status_index ON chapters (novel_id, status);
