package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const maxStrikeNoteLength = 1000

// validStrikeSeverities mirrors the DB check constraint (migration
// 0042) — kept in sync manually since Go has no shared enum with SQL.
var validStrikeSeverities = map[string]bool{"minor": true, "moderate": true, "severe": true}

// AuthorModerationService backs the admin report detail view's author
// panel: an author's other novels (read-only context — the Published
// tab investigates, it doesn't act; moderating unreported content goes
// through the main Novels/Chapters admin pages instead, or a fresh
// report), their strike history, and adding a strike. Every method
// takes both the free-text authorName every novel carries AND an
// optional ownerUserID — when the reported novel has a real account
// (migration 0030), ownerUserID is non-nil and every query uses the
// account-keyed path instead; admin-uploaded/unclaimed novels (still
// the common case) keep working exactly as before via the name-keyed
// fallback.
type AuthorModerationService struct {
	novels         *repository.NovelRepository
	comments       *repository.NovelCommentRepository
	strikes        *repository.AuthorStrikeRepository
	reports        *repository.NovelReportRepository
	users          *repository.UserRepository
	authorProfiles *repository.AuthorProfileRepository
	notifications  *repository.NotificationRepository
	events         realtime.Publisher
}

func NewAuthorModerationService(
	novels *repository.NovelRepository,
	comments *repository.NovelCommentRepository,
	strikes *repository.AuthorStrikeRepository,
	reports *repository.NovelReportRepository,
	users *repository.UserRepository,
	authorProfiles *repository.AuthorProfileRepository,
	notifications *repository.NotificationRepository,
	events realtime.Publisher,
) *AuthorModerationService {
	return &AuthorModerationService{
		novels: novels, comments: comments, strikes: strikes, reports: reports,
		users: users, authorProfiles: authorProfiles, notifications: notifications, events: events,
	}
}

// AuthorProfileSummary is the report detail drawer's Profile tab
// context beyond identity/strikes: account status (only meaningful for
// a real linked account — see ownerUserID's doc comment above) and a
// catalog-wide rollup so an admin gets a sense of scale before opening
// the Published tab's full list.
type AuthorProfileSummary struct {
	// HasAccount is false for an admin-uploaded/unclaimed novel with no
	// real owner — IsBanned/JoinedAt/PenName/Bio are all zero-valued
	// then, since there's no account to carry them.
	HasAccount            bool
	IsBanned              bool
	JoinedAt              time.Time
	PenName               string
	Bio                   string
	NovelCount            int
	PublishedChapterCount int
	TotalViews            int64
}

// Profile assembles the Profile tab's extra context in one call.
// Deliberately read-only — banning/unbanning stays on the admin Users
// page (its own accountable surface), this just shows current status
// so an admin reviewing a report is never blind to it.
func (service *AuthorModerationService) Profile(ctx context.Context, authorName string, ownerUserID *string) (*AuthorProfileSummary, error) {
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

	summary := &AuthorProfileSummary{NovelCount: len(novels)}
	for _, novel := range novels {
		summary.PublishedChapterCount += novel.PublishedChapters
		summary.TotalViews += novel.ViewCount
	}

	if ownerUserID == nil {
		return summary, nil
	}
	summary.HasAccount = true

	user, err := service.users.FindByID(ctx, *ownerUserID)
	if err != nil {
		return nil, err
	}
	summary.IsBanned = user.IsBanned
	summary.JoinedAt = user.CreatedAt

	authorProfile, err := service.authorProfiles.GetByUserID(ctx, *ownerUserID)
	if err != nil && !errors.Is(err, repository.ErrAuthorProfileNotFound) {
		return nil, err
	}
	if authorProfile != nil {
		summary.PenName = authorProfile.PenName
		summary.Bio = authorProfile.Bio
	}
	return summary, nil
}

// OtherNovels lists an author's catalog, excluding the novel the admin
// was already looking at (the one on the report being reviewed), plus
// each novel's top-level comment count (keyed by novel id; a novel
// with zero comments is simply absent — see
// NovelCommentRepository.CountForNovels) for the Published tab's
// stats display.
func (service *AuthorModerationService) OtherNovels(ctx context.Context, authorName string, ownerUserID *string, excludeNovelID string) ([]*repository.Novel, map[string]int, error) {
	var novels []*repository.Novel
	var err error
	if ownerUserID != nil {
		novels, err = service.novels.ListByOwnerUserID(ctx, *ownerUserID)
	} else {
		novels, err = service.novels.ListByAuthorName(ctx, authorName)
	}
	if err != nil {
		return nil, nil, err
	}
	filtered := make([]*repository.Novel, 0, len(novels))
	novelIDs := make([]string, 0, len(novels))
	for _, novel := range novels {
		if novel.ID != excludeNovelID {
			filtered = append(filtered, novel)
			novelIDs = append(novelIDs, novel.ID)
		}
	}
	commentCounts, err := service.comments.CountForNovels(ctx, novelIDs)
	if err != nil {
		return nil, nil, err
	}
	return filtered, commentCounts, nil
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

// AddStrike records the strike and, when it's against a real account
// (ownerUserID non-nil), notifies the author's inbox — a strike no one
// is told about isn't a warning, it's just an internal note. Purely
// informational otherwise: no automatic ban/restriction follows from
// any number or severity of strikes (that stays a manual admin call
// on the Users page).
func (service *AuthorModerationService) AddStrike(ctx context.Context, authorName string, ownerUserID *string, note, severity, adminID string) (*repository.AuthorStrike, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, &ValidationError{Message: "strike note cannot be empty"}
	}
	if len(note) > maxStrikeNoteLength {
		return nil, &ValidationError{Message: "strike note too long"}
	}
	if !validStrikeSeverities[severity] {
		return nil, &ValidationError{Message: "invalid strike severity"}
	}

	strike, err := service.strikes.Create(ctx, authorName, ownerUserID, note, severity, adminID)
	if err != nil {
		return nil, err
	}

	if ownerUserID != nil {
		title := fmt.Sprintf("A %s strike was recorded on your account", severity)
		if err := service.notifications.CreateForUserGeneral(ctx, *ownerUserID, title, note); err == nil {
			service.events.Publish(realtime.Event{Topic: "notification.new"})
		}
	}

	return strike, nil
}

