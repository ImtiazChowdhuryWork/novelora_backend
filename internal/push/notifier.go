package push

import "context"

// Notifier sends a "new chapter" push to a set of device tokens.
// Never returns an error: publishing a chapter must never fail because
// a push provider is down — implementations log failures internally.
// Mirrors realtime.Publisher's interface-first, swappable-transport shape.
type Notifier interface {
	// novelID rides along as data (not shown in the notification itself)
	// so a tap on the client can navigate straight to that novel.
	NotifyNewChapter(ctx context.Context, tokens []string, novelID, novelTitle, chapterTitle string, chapterNumber int)

	// NotifyBroadcast sends a general announcement with no novel behind
	// it — a tap just opens the inbox rather than navigating anywhere.
	NotifyBroadcast(ctx context.Context, tokens []string, title, body string)
}
