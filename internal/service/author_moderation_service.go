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
// strike, and bulk-hiding every novel by that name. Every method takes
// both the free-text authorName every novel carries AND an optional
// ownerUserID — when the reported novel has a real account (migration
// 0030), ownerUserID is non-nil and every query uses the account-keyed
// path instead; admin-uploaded/unclaimed novels (still the common
// case) keep working exactly as before via the name-keyed fallback.
type AuthorModerationService struct {
	novels  *repository.NovelRepository
	strikes *repository.AuthorStrikeRepository
	reports *repository.NovelReportRepository
	events  realtime.Publisher
}

func NewAuthorModerationService(novels *repository.NovelRepository, strikes *repository.AuthorStrikeRepository, reports *repository.NovelReportRepository, events realtime.Publisher) *AuthorModerationService {
	return &AuthorModerationService{novels: novels, strikes: strikes, reports: reports, events: events}
}

// OtherNovels lists an author's catalog, excluding the novel the admin
// was already looking at (the one on the report being reviewed).
func (service *AuthorModerationService) OtherNovels(ctx context.Context, authorName string, ownerUserID *string, excludeNovelID string) ([]*repository.Novel, error) {
	var novels []*repository.Novel
	var err error
	if ownerUserID != nil {
		novels, err = service.novels.ListByOwnerUserID(ctx, *ownerUserID)
	} else {
		novels, err = service.novels.ListByAuthorName(ctx, authorName)
	}
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

func (service *AuthorModerationService) Strikes(ctx context.Context, authorName string, ownerUserID *string) ([]*repository.AuthorStrike, error) {
	if ownerUserID != nil {
		return service.strikes.ListForOwner(ctx, *ownerUserID)
	}
	return service.strikes.ListForAuthor(ctx, authorName)
}

// Reports is the report-history section of the report detail drawer —
// every past report against this author's catalog, so the admin can
// see patterns before acting.
func (service *AuthorModerationService) Reports(ctx context.Context, authorName string, ownerUserID *string) ([]*repository.NovelReport, error) {
	if ownerUserID != nil {
		return service.reports.ListForOwnerAll(ctx, *ownerUserID)
	}
	return service.reports.ListForAuthorName(ctx, authorName)
}

func (service *AuthorModerationService) AddStrike(ctx context.Context, authorName string, ownerUserID *string, note, adminID string) (*repository.AuthorStrike, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, &ValidationError{Message: "strike note cannot be empty"}
	}
	if len(note) > maxStrikeNoteLength {
		return nil, &ValidationError{Message: "strike note too long"}
	}
	return service.strikes.Create(ctx, authorName, ownerUserID, note, adminID)
}

// BulkHide is the "take action against the author" enforcement path —
// hides every novel by this author (account-keyed when available,
// name-keyed otherwise) in one action, publishing the same
// "novel.deleted" event a single-novel delete does for each one (see
// NovelService.Delete) so the app and dashboard both refresh. Returns
// how many were hidden (0 is valid: everything was already hidden, or
// there was nothing to hide).
func (service *AuthorModerationService) BulkHide(ctx context.Context, authorName string, ownerUserID *string) (int, error) {
	var ids []string
	var err error
	if ownerUserID != nil {
		ids, err = service.novels.BulkHideByOwnerUserID(ctx, *ownerUserID)
	} else {
		ids, err = service.novels.BulkHideByAuthorName(ctx, authorName)
	}
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		service.events.Publish(realtime.Event{Topic: "novel.deleted", ID: id})
	}
	return len(ids), nil
}
