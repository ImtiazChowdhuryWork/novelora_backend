package push

import (
	"context"
	"log"
)

// NoopNotifier is used when FIREBASE_CREDENTIALS_JSON is not configured.
// Publishing chapters keeps working; notifications are just skipped.
type NoopNotifier struct{}

func (NoopNotifier) NotifyNewChapter(_ context.Context, _ []string, _, novelTitle, _ string, _ int) {
	log.Printf("push: not configured (FIREBASE_CREDENTIALS_JSON unset) — skipped notification for %q", novelTitle)
}
