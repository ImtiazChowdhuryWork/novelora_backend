package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// OutboxEvent is a durably-queued side effect (currently the two
// notification fan-outs) written in the same request that triggered it,
// so a crash between "business write committed" and "notification sent"
// no longer loses the notification — the next processor tick picks it
// back up. See ChapterService/NovelService's Deliver* methods for what
// consumes each EventType.
type OutboxEvent struct {
	ID          string
	EventType   string
	Payload     []byte
	Attempts    int
	LastError   string
	ProcessedAt *time.Time
	CreatedAt   time.Time
}

type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// Enqueue durably records an event. Called synchronously on the request
// path right after the triggering write succeeds — it's a single fast
// insert, not the slow delivery work itself, so it doesn't meaningfully
// add latency.
func (repository *OutboxRepository) Enqueue(ctx context.Context, eventType string, payload any) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	_, err = repository.pool.Exec(ctx,
		`INSERT INTO outbox_events (event_type, payload) VALUES ($1, $2)`,
		eventType, payloadJSON)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

// ClaimBatch returns the oldest unprocessed events, up to limit. No row
// locking: today there is exactly one process (one ticker) ever reading
// this table, so a plain SELECT is sufficient — add FOR UPDATE SKIP
// LOCKED (inside a short claim transaction) only if a second consumer
// process is ever introduced.
func (repository *OutboxRepository) ClaimBatch(ctx context.Context, limit int) ([]*OutboxEvent, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id, event_type, payload, attempts, last_error, processed_at, created_at
		FROM outbox_events
		WHERE processed_at IS NULL
		ORDER BY created_at
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("claim outbox batch: %w", err)
	}
	defer rows.Close()

	events := []*OutboxEvent{}
	for rows.Next() {
		event := &OutboxEvent{}
		if err := rows.Scan(&event.ID, &event.EventType, &event.Payload,
			&event.Attempts, &event.LastError, &event.ProcessedAt, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (repository *OutboxRepository) MarkProcessed(ctx context.Context, id string) error {
	_, err := repository.pool.Exec(ctx,
		`UPDATE outbox_events SET processed_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark outbox event processed: %w", err)
	}
	return nil
}

func (repository *OutboxRepository) MarkFailed(ctx context.Context, id string, errMessage string) error {
	_, err := repository.pool.Exec(ctx,
		`UPDATE outbox_events SET attempts = attempts + 1, last_error = $2 WHERE id = $1`,
		id, errMessage)
	if err != nil {
		return fmt.Errorf("mark outbox event failed: %w", err)
	}
	return nil
}
