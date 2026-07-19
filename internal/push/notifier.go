package push

import "context"

// Notifier sends a "new chapter" push to a set of device tokens.
// Never returns an error: publishing a chapter must never fail because
// a push provider is down — implementations log failures internally.
// Mirrors realtime.Publisher's interface-first, swappable-transport shape.
type Notifier interface {
	NotifyNewChapter(ctx context.Context, tokens []string, novelTitle, chapterTitle string, chapterNumber int)
}
