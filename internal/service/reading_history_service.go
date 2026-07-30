package service

import (
	"context"
	"fmt"
	"log"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// readingHistoryLimit bounds how far back a reader's Viewed tab looks —
// generous enough to feel complete without ever needing pagination.
const readingHistoryLimit = 100

// ReadingHistoryService is Phase 5a: private, per-user reading history.
// Deliberately not realtime-broadcast (see the plan's Phase 5a note) —
// nobody but the reader themselves needs to know what they're reading,
// and it only needs to be current the next time their own client asks.
type ReadingHistoryService struct {
	history *repository.ReadingHistoryRepository
	novels  *repository.NovelRepository
	genres  *repository.GenreRepository
}

func NewReadingHistoryService(
	history *repository.ReadingHistoryRepository,
	novels *repository.NovelRepository,
	genres *repository.GenreRepository,
) *ReadingHistoryService {
	return &ReadingHistoryService{history: history, novels: novels, genres: genres}
}

// RecordRead is best-effort: called from the chapter GET path, it must
// never fail or slow down the reader's actual request.
func (service *ReadingHistoryService) RecordRead(ctx context.Context, userID, novelID, chapterID string) {
	if err := service.history.Upsert(ctx, userID, novelID, chapterID); err != nil {
		log.Printf("reading history: could not record read for user %s novel %s: %v", userID, novelID, err)
	}
}

// ListForUser resolves userID's reading history into full novel
// records, most-recently-read first — the backing data for Library's
// Viewed tab.
func (service *ReadingHistoryService) ListForUser(ctx context.Context, userID string) ([]*repository.Novel, error) {
	entries, err := service.history.ListForUser(ctx, userID, readingHistoryLimit)
	if err != nil {
		return nil, fmt.Errorf("list reading history: %w", err)
	}
	if len(entries) == 0 {
		return []*repository.Novel{}, nil
	}

	novelIDs := make([]string, len(entries))
	for index, entry := range entries {
		novelIDs[index] = entry.NovelID
	}
	novels, err := service.novels.ListByIDs(ctx, novelIDs)
	if err != nil {
		return nil, fmt.Errorf("resolve reading history novels: %w", err)
	}
	byID := make(map[string]*repository.Novel, len(novels))
	for _, novel := range novels {
		byID[novel.ID] = novel
	}

	// ListByIDs doesn't guarantee order — re-sort to the reader's actual
	// last-read-first order, dropping any entry whose novel is gone
	// (deleted since it was read).
	ordered := make([]*repository.Novel, 0, len(entries))
	for _, entry := range entries {
		if novel, ok := byID[entry.NovelID]; ok {
			ordered = append(ordered, novel)
		}
	}

	if err := attachGenres(ctx, service.genres, ordered); err != nil {
		return nil, fmt.Errorf("attach genres: %w", err)
	}
	return ordered, nil
}
