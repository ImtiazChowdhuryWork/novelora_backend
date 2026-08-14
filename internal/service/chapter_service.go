package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/push"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const (
	maxChapterTitleLength   = 255
	maxChapterContentLength = 1 << 20 // 1MB of text per chapter
	maxImportBatchSize      = 500
)

type ChapterService struct {
	chapters      *repository.ChapterRepository
	novels        *repository.NovelRepository
	deviceTokens  *repository.DeviceTokenRepository
	notifications *repository.NotificationRepository
	notifier      push.Notifier
	events        realtime.Publisher
	outbox        *repository.OutboxRepository
}

func NewChapterService(
	chapters *repository.ChapterRepository,
	novels *repository.NovelRepository,
	deviceTokens *repository.DeviceTokenRepository,
	notifications *repository.NotificationRepository,
	notifier push.Notifier,
	events realtime.Publisher,
	outbox *repository.OutboxRepository,
) *ChapterService {
	return &ChapterService{
		chapters:      chapters,
		novels:        novels,
		deviceTokens:  deviceTokens,
		notifications: notifications,
		notifier:      notifier,
		events:        events,
		outbox:        outbox,
	}
}

func (chapterService *ChapterService) ListByNovel(ctx context.Context, novelID string) ([]*repository.Chapter, error) {
	// Listing for a deleted/unknown novel must 404, not return empty
	if _, err := chapterService.novels.GetByID(ctx, novelID); err != nil {
		return nil, err
	}
	return chapterService.chapters.ListByNovel(ctx, novelID, false)
}

// ListPublishedByNovel is the reader-facing listing: drafts never leak.
func (chapterService *ChapterService) ListPublishedByNovel(ctx context.Context, novelID string) ([]*repository.Chapter, error) {
	if _, err := chapterService.novels.GetByID(ctx, novelID); err != nil {
		return nil, err
	}
	return chapterService.chapters.ListByNovel(ctx, novelID, true)
}

func (chapterService *ChapterService) Get(ctx context.Context, chapterID string) (*repository.Chapter, error) {
	return chapterService.chapters.GetByID(ctx, chapterID)
}

// GetPublished is the reader-facing fetch: draft chapters 404.
func (chapterService *ChapterService) GetPublished(ctx context.Context, chapterID string) (*repository.Chapter, error) {
	chapter, err := chapterService.chapters.GetByID(ctx, chapterID)
	if err != nil {
		return nil, err
	}
	if chapter.Status != "published" {
		return nil, repository.ErrChapterNotFound
	}
	return chapter, nil
}

// Import commits the dashboard's import pipeline: any incoming chapter
// whose title matches one this novel already has REPLACES that
// chapter's content in place (status untouched — a published chapter
// stays published); everything else is appended as a new draft.
// Without this, re-running an import (the same PDF, or a corrected
// extraction of it) just piled up duplicate chapters forever.
func (chapterService *ChapterService) Import(ctx context.Context, novelID string, writes []repository.ChapterWrite) (created, updated int, err error) {
	if _, err := chapterService.novels.GetByID(ctx, novelID); err != nil {
		return 0, 0, err
	}
	if len(writes) == 0 {
		return 0, 0, &ValidationError{Message: "no chapters to import"}
	}
	if len(writes) > maxImportBatchSize {
		return 0, 0, &ValidationError{Message: fmt.Sprintf("too many chapters in one import (max %d)", maxImportBatchSize)}
	}
	for index := range writes {
		if err := validateChapterWrite(&writes[index]); err != nil {
			return 0, 0, err
		}
	}

	existing, err := chapterService.chapters.ListByNovel(ctx, novelID, false)
	if err != nil {
		return 0, 0, err
	}
	existingByTitle := make(map[string]*repository.Chapter, len(existing))
	for _, chapter := range existing {
		existingByTitle[normalizeChapterTitle(chapter.Title)] = chapter
	}

	toCreate := make([]repository.ChapterWrite, 0, len(writes))
	for _, write := range writes {
		match, isReplace := existingByTitle[normalizeChapterTitle(write.Title)]
		if !isReplace {
			toCreate = append(toCreate, write)
			continue
		}
		if _, err := chapterService.chapters.Update(ctx, match.ID, write); err != nil {
			return created, updated, err
		}
		updated++
	}

	if len(toCreate) > 0 {
		created, err = chapterService.chapters.CreateBatch(ctx, novelID, toCreate)
		if err != nil {
			return created, updated, err
		}
	}

	chapterService.publishChapterChangedEvents(novelID, "chapter.updated")
	return created, updated, nil
}

// normalizeChapterTitle makes import-time title matching tolerant of
// whitespace/casing differences a re-extraction might introduce.
func normalizeChapterTitle(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(title), " "))
}

// CreateOne appends a single draft chapter (manual authoring) and
// returns it, so the caller can respond with the real created resource.
func (chapterService *ChapterService) CreateOne(ctx context.Context, novelID string, write repository.ChapterWrite) (*repository.Chapter, error) {
	if _, err := chapterService.novels.GetByID(ctx, novelID); err != nil {
		return nil, err
	}
	if err := validateChapterWrite(&write); err != nil {
		return nil, err
	}

	chapter, err := chapterService.chapters.Create(ctx, novelID, write)
	if err != nil {
		return nil, err
	}
	chapterService.publishChapterChangedEvents(novelID, "chapter.updated")
	return chapter, nil
}

// publishChapterChangedEvents is the shared notification for anything
// that adds/edits chapters: the chapter list and the novel's chapter
// counts both need to refresh.
func (chapterService *ChapterService) publishChapterChangedEvents(novelID, chapterTopic string) {
	chapterService.events.Publish(realtime.Event{Topic: chapterTopic, ID: novelID})
	chapterService.events.Publish(realtime.Event{Topic: "novel.updated", ID: novelID})
}

func (chapterService *ChapterService) Update(ctx context.Context, chapterID string, write repository.ChapterWrite) (*repository.Chapter, error) {
	if err := validateChapterWrite(&write); err != nil {
		return nil, err
	}
	chapter, err := chapterService.chapters.Update(ctx, chapterID, write)
	if err != nil {
		return nil, err
	}
	chapterService.events.Publish(realtime.Event{Topic: "chapter.updated", ID: chapter.NovelID})
	return chapter, nil
}

func (chapterService *ChapterService) UpdateStatus(ctx context.Context, chapterID, status string) (*repository.Chapter, error) {
	if status != "draft" && status != "published" {
		return nil, &ValidationError{Message: "status must be 'draft' or 'published'"}
	}
	chapter, err := chapterService.chapters.UpdateStatus(ctx, chapterID, status)
	if err != nil {
		return nil, err
	}
	chapterTopic := "chapter.updated"
	if status == "published" {
		chapterTopic = "chapter.published"
		if err := chapterService.outbox.Enqueue(ctx, "chapter.published", chapterPublishedPayload{ChapterID: chapter.ID}); err != nil {
			log.Printf("outbox: could not enqueue chapter.published for %s: %v", chapter.ID, err)
		}
	}
	chapterService.publishChapterChangedEvents(chapter.NovelID, chapterTopic)
	return chapter, nil
}

// Schedule queues a draft chapter to auto-publish at scheduledAt.
// Publishing already has its own path (UpdateStatus) — this only ever
// touches drafts.
func (chapterService *ChapterService) Schedule(ctx context.Context, chapterID string, scheduledAt time.Time) (*repository.Chapter, error) {
	if !scheduledAt.After(time.Now()) {
		return nil, &ValidationError{Message: "scheduled time must be in the future"}
	}
	chapter, err := chapterService.chapters.GetByID(ctx, chapterID)
	if err != nil {
		return nil, err
	}
	if chapter.Status != "draft" {
		return nil, &ValidationError{Message: "only draft chapters can be scheduled"}
	}
	chapter, err = chapterService.chapters.SetScheduledAt(ctx, chapterID, &scheduledAt)
	if err != nil {
		return nil, err
	}
	chapterService.events.Publish(realtime.Event{Topic: "chapter.updated", ID: chapter.NovelID})
	return chapter, nil
}

// Unschedule clears a pending schedule; the chapter stays a draft.
func (chapterService *ChapterService) Unschedule(ctx context.Context, chapterID string) (*repository.Chapter, error) {
	chapter, err := chapterService.chapters.SetScheduledAt(ctx, chapterID, nil)
	if err != nil {
		return nil, err
	}
	chapterService.events.Publish(realtime.Event{Topic: "chapter.updated", ID: chapter.NovelID})
	return chapter, nil
}

// PublishDueScheduled auto-publishes every draft whose schedule has
// arrived. Called periodically by a background ticker (see main.go).
// Reuses UpdateStatus so a scheduled publish behaves identically to a
// manual one — push, inbox, and realtime all fire the same way.
func (chapterService *ChapterService) PublishDueScheduled(ctx context.Context) (int, error) {
	due, err := chapterService.chapters.ListDueForPublish(ctx, time.Now())
	if err != nil {
		return 0, err
	}
	for _, chapter := range due {
		if _, err := chapterService.UpdateStatus(ctx, chapter.ID, "published"); err != nil {
			log.Printf("schedule: could not auto-publish chapter %s: %v", chapter.ID, err)
		}
	}
	return len(due), nil
}

// chapterPublishedPayload is the outbox payload for a "chapter.published"
// event — just enough to re-fetch fresh state at delivery time, since
// the chapter/novel could change between enqueue and delivery.
type chapterPublishedPayload struct {
	ChapterID string `json:"chapter_id"`
}

// DeliverChapterPublishedNotification is called by the outbox processor
// (see OutboxProcessor), never directly from the request path — that's
// what makes it durable: it survives a process crash between the
// publish committing and the notification actually going out, unlike
// the fire-and-forget goroutine this replaced. Populates both delivery
// paths: the FCM push (device tray) and the in-app inbox (notifications
// table) — independently, so a failure in one doesn't skip the other.
func (chapterService *ChapterService) DeliverChapterPublishedNotification(ctx context.Context, payload []byte) error {
	var decoded chapterPublishedPayload
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return fmt.Errorf("unmarshal chapter.published payload: %w", err)
	}

	chapter, err := chapterService.chapters.GetByID(ctx, decoded.ChapterID)
	if err != nil {
		if err == repository.ErrChapterNotFound {
			log.Printf("outbox: chapter %s no longer exists, skipping notification", decoded.ChapterID)
			return nil
		}
		return fmt.Errorf("load chapter %s: %w", decoded.ChapterID, err)
	}

	novel, err := chapterService.novels.GetByID(ctx, chapter.NovelID)
	if err != nil {
		if err == repository.ErrNovelNotFound {
			log.Printf("outbox: novel %s no longer exists, skipping notification", chapter.NovelID)
			return nil
		}
		return fmt.Errorf("load novel %s: %w", chapter.NovelID, err)
	}

	tokens, err := chapterService.deviceTokens.ListAllTokens(ctx)
	if err != nil {
		log.Printf("push: could not load device tokens: %v", err)
	} else {
		chapterService.notifier.NotifyNewChapter(ctx, tokens, novel.ID, novel.Title, chapter.Title, chapter.Number)
	}

	body := fmt.Sprintf("Chapter %d is now available", chapter.Number)
	if chapter.Title != "" {
		body += ": " + chapter.Title
	}
	if err := chapterService.notifications.CreateForAllUsers(ctx, novel.ID, chapter.ID, novel.Title, body); err != nil {
		return fmt.Errorf("create inbox notifications for chapter %s: %w", chapter.ID, err)
	}
	chapterService.events.Publish(realtime.Event{Topic: "notification.new"})
	return nil
}

func (chapterService *ChapterService) Delete(ctx context.Context, chapterID string) error {
	chapter, err := chapterService.chapters.GetByID(ctx, chapterID)
	if err != nil {
		return err
	}
	if err := chapterService.chapters.Delete(ctx, chapterID); err != nil {
		return err
	}
	chapterService.publishChapterChangedEvents(chapter.NovelID, "chapter.updated")
	return nil
}

func validateChapterWrite(write *repository.ChapterWrite) error {
	write.Title = strings.TrimSpace(write.Title)
	if len(write.Title) > maxChapterTitleLength {
		return &ValidationError{Message: fmt.Sprintf("chapter title too long (max %d characters)", maxChapterTitleLength)}
	}
	if strings.TrimSpace(write.ContentText) == "" {
		return &ValidationError{Message: "chapter content is empty"}
	}
	if len(write.ContentText) > maxChapterContentLength {
		return &ValidationError{Message: "chapter content too large (max 1MB)"}
	}
	if write.ContentJSON == "" {
		write.ContentJSON = "{}"
	}
	if !json.Valid([]byte(write.ContentJSON)) {
		return &ValidationError{Message: "chapter content_json is not valid JSON"}
	}
	// The server owns the word count — clients don't get to lie about it
	write.WordCount = len(strings.Fields(write.ContentText))
	return nil
}
