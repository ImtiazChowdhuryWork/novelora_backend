package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuditLog is one recorded admin action. ActorID is nullable (and the
// FK is ON DELETE SET NULL) so a deleted admin account doesn't destroy
// the trail — ActorName is stored denormalized precisely so "who did
// this" stays readable even then.
type AuditLog struct {
	ID         string
	ActorID    *string
	ActorName  string
	Action     string // e.g. "novel.created", "user.banned"
	EntityType string // "novel", "chapter", "genre", "user"
	EntityID   string
	Details    map[string]any
	CreatedAt  time.Time
}

type AuditLogRepository struct {
	pool *pgxpool.Pool
}

func NewAuditLogRepository(pool *pgxpool.Pool) *AuditLogRepository {
	return &AuditLogRepository{pool: pool}
}

func (repository *AuditLogRepository) Create(ctx context.Context, entry AuditLog) error {
	detailsJSON, err := json.Marshal(entry.Details)
	if err != nil {
		return fmt.Errorf("marshal audit details: %w", err)
	}
	_, err = repository.pool.Exec(ctx, `
		INSERT INTO audit_logs (actor_id, actor_name, action, entity_type, entity_id, details)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		entry.ActorID, entry.ActorName, entry.Action, entry.EntityType, entry.EntityID, detailsJSON)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

// AuditLogListFilter narrows and pages the audit log list.
type AuditLogListFilter struct {
	Action     string
	EntityType string
	Page       int
	PageSize   int
}

func (repository *AuditLogRepository) List(ctx context.Context, filter AuditLogListFilter) ([]*AuditLog, int, error) {
	conditions := []string{"true"}
	arguments := []any{}

	if filter.Action != "" {
		arguments = append(arguments, filter.Action)
		conditions = append(conditions, fmt.Sprintf("action = $%d", len(arguments)))
	}
	if filter.EntityType != "" {
		arguments = append(arguments, filter.EntityType)
		conditions = append(conditions, fmt.Sprintf("entity_type = $%d", len(arguments)))
	}
	whereClause := strings.Join(conditions, " AND ")

	var total int
	if err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM audit_logs WHERE "+whereClause, arguments...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}

	arguments = append(arguments, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := repository.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, actor_id, actor_name, action, entity_type, entity_id, details, created_at
		FROM audit_logs WHERE %s
		ORDER BY created_at DESC LIMIT $%d OFFSET $%d`,
		whereClause, len(arguments)-1, len(arguments)), arguments...)
	if err != nil {
		return nil, 0, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	logs := []*AuditLog{}
	for rows.Next() {
		entry := &AuditLog{}
		var detailsJSON []byte
		if err := rows.Scan(&entry.ID, &entry.ActorID, &entry.ActorName, &entry.Action,
			&entry.EntityType, &entry.EntityID, &detailsJSON, &entry.CreatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan audit log: %w", err)
		}
		if len(detailsJSON) > 0 {
			if err := json.Unmarshal(detailsJSON, &entry.Details); err != nil {
				return nil, 0, fmt.Errorf("unmarshal audit details: %w", err)
			}
		}
		logs = append(logs, entry)
	}
	return logs, total, rows.Err()
}
