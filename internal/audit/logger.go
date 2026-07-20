// Package audit records admin actions for accountability: who did
// what, to which resource, and when.
package audit

import (
	"context"
	"log"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// Logger is fire-and-forget by design, the same rationale as the push
// notifier: recording an action must never slow down or fail the
// admin request that triggered it.
type Logger struct {
	entries *repository.AuditLogRepository
}

func NewLogger(entries *repository.AuditLogRepository) *Logger {
	return &Logger{entries: entries}
}

// Log records one action. actorID may be empty (defensive only — every
// call site today is behind Authenticate, so it's always populated).
func (logger *Logger) Log(actorID, actorName, action, entityType, entityID string, details map[string]any) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var actorIDPointer *string
		if actorID != "" {
			actorIDPointer = &actorID
		}
		entry := repository.AuditLog{
			ActorID:    actorIDPointer,
			ActorName:  actorName,
			Action:     action,
			EntityType: entityType,
			EntityID:   entityID,
			Details:    details,
		}
		if err := logger.entries.Create(ctx, entry); err != nil {
			log.Printf("audit: failed to log %s on %s %s: %v", action, entityType, entityID, err)
		}
	}()
}
