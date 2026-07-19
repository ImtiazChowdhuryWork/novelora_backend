package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// actorFromContext reads the authenticated caller's id/name, set by
// middleware.Authenticate. Shared by every handler that writes audit
// log entries.
func actorFromContext(ctx context.Context) (id, name string) {
	id, _ = ctx.Value(middleware.UserIDContextKey).(string)
	name, _ = ctx.Value(middleware.UserNameContextKey).(string)
	return id, name
}

type AdminAuditLogHandler struct {
	logs *repository.AuditLogRepository
}

func NewAdminAuditLogHandler(logs *repository.AuditLogRepository) *AdminAuditLogHandler {
	return &AdminAuditLogHandler{logs: logs}
}

type auditLogResponse struct {
	ID         string         `json:"id"`
	ActorID    *string        `json:"actor_id"`
	ActorName  string         `json:"actor_name"`
	Action     string         `json:"action"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Details    map[string]any `json:"details"`
	CreatedAt  time.Time      `json:"created_at"`
}

func newAuditLogResponse(entry *repository.AuditLog) auditLogResponse {
	return auditLogResponse{
		ID:         entry.ID,
		ActorID:    entry.ActorID,
		ActorName:  entry.ActorName,
		Action:     entry.Action,
		EntityType: entry.EntityType,
		EntityID:   entry.EntityID,
		Details:    entry.Details,
		CreatedAt:  entry.CreatedAt,
	}
}

// List: GET /admin/audit-logs?page=&page_size=&action=&entity_type=
func (adminAuditLogHandler *AdminAuditLogHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	entries, total, err := adminAuditLogHandler.logs.List(request.Context(), repository.AuditLogListFilter{
		Action:     query.Get("action"),
		EntityType: query.Get("entity_type"),
		Page:       page,
		PageSize:   pageSize,
	})
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	items := make([]auditLogResponse, 0, len(entries))
	for _, entry := range entries {
		items = append(items, newAuditLogResponse(entry))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"items": items,
		"total": total,
	})
}
