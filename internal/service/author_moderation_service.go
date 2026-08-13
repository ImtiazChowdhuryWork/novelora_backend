package service

import (
	"context"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const maxStrikeNoteLength = 1000

// AuthorModerationService backs the admin report detail view's author
// panel: an author's other novels, their strike history, adding a
// strike, and bulk-hiding every novel by that name. Everything here is
// keyed by the free-text author_name novels already carry — see the
// migration's comment on why (no author-account system yet).
type AuthorModerationService struct {
	novels  *repository.NovelRepository
	strikes *repository.AuthorStrikeRepository
	events  realtime.Publisher
}

func NewAuthorModerationService(novels *repository.NovelRepository, strikes *repository.AuthorStrikeRepository, events realtime.Publisher) *AuthorModerationService {
	return &AuthorModerationService{novels: novels, strikes: strikes, events: events}
}

// OtherNovels lists an author's catalog, excluding the novel the admin
// was already looking at (the one on the report being reviewed).
func (service *AuthorModerationService) OtherNovels(ctx context.Context, authorName, excludeNovelID string) ([]*repository.Novel, error) {
	novels, err := service.novels.ListByAuthorName(ctx, authorName)
	if err != nil {
		return nil, err
	}
	filtered := make([]*repository.Novel, 0, len(novels))
	for _, novel := range novels {
		if novel.ID != excludeNovelID {
			filtered = append(filtered, novel)
		}
	}
	return filtered, nil
}

func (service *AuthorModerationService) Strikes(ctx context.Context, authorName string) ([]*repository.AuthorStrike, error) {
	return service.strikes.ListForAuthor(ctx, authorName)
}

func (service *AuthorModerationService) AddStrike(ctx context.Context, authorName, note, adminID string) (*repository.AuthorStrike, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, &ValidationError{Message: "strike note cannot be empty"}
	}
	if len(note) > maxStrikeNoteLength {
		return nil, &ValidationError{Message: "strike note too long"}
	}
	return service.strikes.Create(ctx, authorName, note, adminID)
}

// BulkHide is the "take action against the author" enforcement path —
// hides every novel by this author name in one action, publishing the
// same "novel.deleted" event a single-novel delete does for each one
// (see NovelService.Delete) so the app and dashboard both refresh.
// Returns how many were hidden (0 is valid: every novel by this name
// was already hidden, or the name never had any).
func (service *AuthorModerationService) BulkHide(ctx context.Context, authorName string) (int, error) {
	ids, err := service.novels.BulkHideByAuthorName(ctx, authorName)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		service.events.Publish(realtime.Event{Topic: "novel.deleted", ID: id})
	}
	return len(ids), nil
}
