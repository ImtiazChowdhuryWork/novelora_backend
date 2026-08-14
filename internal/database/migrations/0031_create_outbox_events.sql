-- Durable outbox for the two notification fan-outs (chapter published,
-- novel created) that previously ran as fire-and-forget goroutines and
-- were silently lost on a process crash/restart mid-delivery. A ticker
-- (see runOutboxProcessorTicker) polls this table instead.

CREATE TABLE outbox_events (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type   text        NOT NULL,
    payload      jsonb       NOT NULL,
    attempts     int         NOT NULL DEFAULT 0,
    last_error   text        NOT NULL DEFAULT '',
    processed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX outbox_events_pending_index ON outbox_events (created_at) WHERE processed_at IS NULL;
