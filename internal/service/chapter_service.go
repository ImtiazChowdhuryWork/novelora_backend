package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const (
	maxChapterTitleLength   = 255
	maxChapterContentLength = 1 << 20 // 1MB of text per chapter
	maxImportBatchSize      = 500
)

type ChapterService struct {
	chapters *repository.ChapterRepository
	novels   *repository.NovelRepository
	events   realtime.Publisher
}

func NewChapterService(
	chapters *repository.ChapterRepository,
	novels *repository.NovelRepository,
	events realtime.Publisher,
) *ChapterService {
	return &ChapterService{chapters: chapters, novels: novels, events: events}
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

// Import appends a batch of draft chapters to a novel (the commit step
// of the dashboard's import pipeline).
func (chapterService *ChapterService) Import(ctx context.Context, novelID string, writes []repository.ChapterWrite) (int, error) {
	if _, err := chapterService.novels.GetByID(ctx, novelID); err != nil {
		return 0, err
	}
	if len(writes) == 0 {
		return 0, &ValidationError{Message: "no chapters to import"}
	}
	if len(writes) > maxImportBatchSize {
		return 0, &ValidationError{Message: fmt.Sprintf("too many chapters in one import (max %d)", maxImportBatchSize)}
	}
	for index := range writes {
		if err := validateChapterWrite(&writes[index]); err != nil {
			return 0, err
		}
	}

	created, err := chapterService.chapters.CreateBatch(ctx, novelID, writes)
	if err != nil {
		return 0, err
	}
	chapterService.publishChapterChangedEvents(novelID, "chapter.updated")
	return created, nil
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
	}
	chapterService.publishChapterChangedEvents(chapter.NovelID, chapterTopic)
	return chapter, nil
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
