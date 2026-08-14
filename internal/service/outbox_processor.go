package service

import (
	"context"
	"fmt"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// OutboxProcessor drains the outbox_events table, dispatching each event
// to the service method that actually delivers it. Driven by a ticker
// (see runOutboxProcessorTicker in cmd/server/main.go), the same
// periodic-background-work shape as scheduled publishing and ranking
// detection.
type OutboxProcessor struct {
	outbox   *repository.OutboxRepository
	chapters *ChapterService
	novels   *NovelService
}

func NewOutboxProcessor(outbox *repository.OutboxRepository, chapters *ChapterService, novels *NovelService) *OutboxProcessor {
	return &OutboxProcessor{outbox: outbox, chapters: chapters, novels: novels}
}

const outboxBatchSize = 20

// ProcessBatch claims and delivers up to one batch of pending events,
// returning how many were claimed. A delivery failure marks the event
// failed (attempts incremented, error recorded) rather than deleting it —
// it's retried on the next tick, and a permanent failure still leaves a
// paper trail instead of vanishing silently.
func (processor *OutboxProcessor) ProcessBatch(ctx context.Context) (int, error) {
	events, err := processor.outbox.ClaimBatch(ctx, outboxBatchSize)
	if err != nil {
		return 0, fmt.Errorf("claim outbox batch: %w", err)
	}

	for _, event := range events {
		var deliverErr error
		switch event.EventType {
		case "chapter.published":
			deliverErr = processor.chapters.DeliverChapterPublishedNotification(ctx, event.Payload)
		case "novel.created":
			deliverErr = processor.novels.DeliverNovelCreatedNotification(ctx, event.Payload)
		default:
			deliverErr = fmt.Errorf("unknown outbox event type %q", event.EventType)
		}

		if deliverErr != nil {
			if err := processor.outbox.MarkFailed(ctx, event.ID, deliverErr.Error()); err != nil {
				return len(events), fmt.Errorf("mark outbox event %s failed: %w", event.ID, err)
			}
			continue
		}
		if err := processor.outbox.MarkProcessed(ctx, event.ID); err != nil {
			return len(events), fmt.Errorf("mark outbox event %s processed: %w", event.ID, err)
		}
	}
	return len(events), nil
}
