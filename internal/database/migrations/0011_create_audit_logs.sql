CREATE TABLE audit_logs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    actor_name  text        NOT NULL,
    action      text        NOT NULL,
    entity_type text        NOT NULL,
    entity_id   text        NOT NULL,
    details     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_logs_created_at_index ON audit_logs (created_at DESC);
CREATE INDEX audit_logs_action_index ON audit_logs (action);
CREATE INDEX audit_logs_entity_type_index ON audit_logs (entity_type);
