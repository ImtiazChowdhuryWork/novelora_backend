package service

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/push"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const (
	maxNovelTitleLength  = 255
	maxNovelAuthorLength = 255
)

type NovelService struct {
	novels         *repository.NovelRepository
	genres         *repository.GenreRepository
	ratings        *repository.NovelRatingRepository
	supports       *repository.NovelSupportRepository
	readingHistory *repository.ReadingHistoryRepository
	deviceTokens   *repository.DeviceTokenRepository
	notifications  *repository.NotificationRepository
	notifier       push.Notifier
	events         realtime.Publisher
}

func NewNovelService(
	novels *repository.NovelRepository,
	genres *repository.GenreRepository,
	ratings *repository.NovelRatingRepository,
	supports *repository.NovelSupportRepository,
	readingHistory *repository.ReadingHistoryRepository,
	deviceTokens *repository.DeviceTokenRepository,
	notifications *repository.NotificationRepository,
	notifier push.Notifier,
	events realtime.Publisher,
) *NovelService {
	return &NovelService{
		novels:         novels,
		genres:         genres,
		ratings:        ratings,
		supports:       supports,
		readingHistory: readingHistory,
		deviceTokens:   deviceTokens,
		notifications:  notifications,
		notifier:       notifier,
		events:         events,
	}
}

func (novelService *NovelService) List(ctx context.Context, filter repository.NovelListFilter) ([]*repository.Novel, int, error) {
	if filter.Page < 1 {
		filter.Page = 1
	}
	switch {
	case filter.PageSize < 1:
		filter.PageSize = 20
	case filter.PageSize > 500:
		// Clamp down rather than resetting to the default — the admin
		// dashboard's drag-to-reorder view asks for a large page size on
		// purpose (it needs the whole catalog loaded, not just page 1).
		filter.PageSize = 500
	}
	if filter.Status != "" && filter.Status != "ongoing" && filter.Status != "completed" {
		return nil, 0, &ValidationError{Message: "status must be 'ongoing' or 'completed'"}
	}
	novels, total, err := novelService.novels.List(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	if err := novelService.attachGenres(ctx, novels); err != nil {
		return nil, 0, err
	}
	return novels, total, nil
}

// personalizationGenreCount is Phase 5e's "top 2-3 genres" — the plan's
// own phrasing; 3 read as the inclusive end of that range.
const personalizationGenreCount = 3

// PersonalizeFilter is Phase 5e: layers a logged-in reader's own top
// genres (by reading_history frequency) onto filter as an "any of
// these" match, reusing NovelListFilter's existing GenreIDs/any
// mechanism (already built for the admin dashboard's multi-select
// filter) rather than inventing a new one. A guest, or a reader with no
// history yet, gets filter back unchanged — List then falls through to
// its ordinary global result, exactly the "nobody ever sees an empty
// section" fallback the plan calls for. Deliberately only fills in
// GenreIDs when the caller hasn't already set one (never overrides an
// explicit request).
func (novelService *NovelService) PersonalizeFilter(ctx context.Context, filter repository.NovelListFilter, userID string) repository.NovelListFilter {
	if userID == "" || filter.GenreID != "" || len(filter.GenreIDs) > 0 {
		return filter
	}
	topGenreIDs, err := novelService.readingHistory.TopGenreIDs(ctx, userID, personalizationGenreCount)
	if err != nil {
		log.Printf("personalize: could not load top genres for user %s: %v", userID, err)
		return filter
	}
	if len(topGenreIDs) == 0 {
		return filter
	}
	filter.GenreIDs = topGenreIDs
	filter.GenreMatchMode = "any"
	return filter
}

func (novelService *NovelService) Get(ctx context.Context, novelID string) (*repository.Novel, error) {
	novel, err := novelService.novels.GetByID(ctx, novelID)
	if err != nil {
		return nil, err
	}
	if err := novelService.attachGenres(ctx, []*repository.Novel{novel}); err != nil {
		return nil, err
	}
	return novel, nil
}

func (novelService *NovelService) Create(ctx context.Context, write repository.NovelWrite, genreIDs []string) (*repository.Novel, error) {
	if err := validateNovelWrite(&write); err != nil {
		return nil, err
	}
	novel, err := novelService.novels.Create(ctx, write)
	if err != nil {
		return nil, err
	}
	if err := novelService.genres.SetForNovel(ctx, novel.ID, genreIDs); err != nil {
		return nil, err
	}
	novel.Genres, err = novelService.genres.ListForNovel(ctx, novel.ID)
	if err != nil {
		return nil, err
	}
	novelService.events.Publish(realtime.Event{Topic: "novel.created", ID: novel.ID})
	go novelService.notifyNewNovelCreated(novel)
	return novel, nil
}

// notifyNewNovelCreated runs on its own timeout-bounded context — never
// the request's — so a slow or failing push provider can't delay or
// fail the create response. Same dual-path shape as
// ChapterService.notifyNewChapterPublished: FCM push and in-app inbox
// populated independently, so a failure in one doesn't skip the other.
func (novelService *NovelService) notifyNewNovelCreated(novel *repository.Novel) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	body := fmt.Sprintf("%q was just added", novel.Title)

	tokens, err := novelService.deviceTokens.ListAllTokens(ctx)
	if err != nil {
		log.Printf("push: could not load device tokens: %v", err)
	} else {
		novelService.notifier.NotifyNovelHighlight(ctx, tokens, novel.ID, novel.Title, body)
	}

	if err := novelService.notifications.CreateNovelNotificationForAllUsers(ctx, novel.ID, novel.Title, body); err != nil {
		log.Printf("inbox: could not create notification for novel %s: %v", novel.ID, err)
		return
	}
	novelService.events.Publish(realtime.Event{Topic: "notification.new"})
}

func (novelService *NovelService) Update(ctx context.Context, novelID string, write repository.NovelWrite, genreIDs []string) (*repository.Novel, error) {
	if err := validateNovelWrite(&write); err != nil {
		return nil, err
	}
	novel, err := novelService.novels.Update(ctx, novelID, write)
	if err != nil {
		return nil, err
	}
	if err := novelService.genres.SetForNovel(ctx, novel.ID, genreIDs); err != nil {
		return nil, err
	}
	novel.Genres, err = novelService.genres.ListForNovel(ctx, novel.ID)
	if err != nil {
		return nil, err
	}
	novelService.events.Publish(realtime.Event{Topic: "novel.updated", ID: novel.ID})
	return novel, nil
}

// RecordView is called by the reader-facing (public) novel handler
// only — never the admin handler, since AdminNovelHandler.Get shares
// NovelService.Get with it and an admin opening the edit form isn't a
// real read. Best-effort: logs and swallows the error rather than
// failing the request that triggered it, since a missed view count
// isn't worth a broken page.
func (novelService *NovelService) RecordView(ctx context.Context, novelID string) {
	if err := novelService.novels.RecordView(ctx, novelID); err != nil {
		log.Printf("record view for novel %s: %v", novelID, err)
	}
}

// Rate submits or updates userID's own 1-5 star rating of novelID, then
// returns the novel with its freshly recomputed aggregate. Deliberately
// not realtime-broadcast — as frequent as view increments, and the
// small aggregate shift isn't worth a refetch signal to every connected
// client; a "rating" section's membership catches up on the existing
// ranking ticker's cadence like any other sort (see Phase 2).
func (novelService *NovelService) Rate(ctx context.Context, novelID, userID string, rating int) (*repository.Novel, error) {
	if rating < 1 || rating > 5 {
		return nil, &ValidationError{Message: "rating must be between 1 and 5"}
	}
	if _, err := novelService.novels.GetByID(ctx, novelID); err != nil {
		return nil, err
	}
	if err := novelService.ratings.Upsert(ctx, novelID, userID, rating); err != nil {
		return nil, err
	}
	return novelService.Get(ctx, novelID)
}

// RemoveRating withdraws userID's own rating of novelID, if any.
func (novelService *NovelService) RemoveRating(ctx context.Context, novelID, userID string) (*repository.Novel, error) {
	if _, err := novelService.novels.GetByID(ctx, novelID); err != nil {
		return nil, err
	}
	if err := novelService.ratings.Remove(ctx, novelID, userID); err != nil {
		return nil, err
	}
	return novelService.Get(ctx, novelID)
}

// GetUserRating is the logged-in caller's own rating of novelID, for
// the novel detail response's "my_rating" — nil if they haven't rated it.
func (novelService *NovelService) GetUserRating(ctx context.Context, novelID, userID string) (*int, error) {
	return novelService.ratings.GetForUser(ctx, novelID, userID)
}

// ListRatings is the admin moderation view: every individual rating on
// novelID, so an admin can identify and remove a single abusive/spam
// one (see AdminRemoveRating) — never hand-edits an existing rating.
func (novelService *NovelService) ListRatings(ctx context.Context, novelID string) ([]*repository.NovelRating, error) {
	return novelService.ratings.ListForNovel(ctx, novelID)
}

// AdminRemoveRating is the moderation counterpart of RemoveRating: an
// admin removing a specific reader's rating rather than a reader
// removing their own.
func (novelService *NovelService) AdminRemoveRating(ctx context.Context, novelID, userID string) (*repository.Novel, error) {
	return novelService.RemoveRating(ctx, novelID, userID)
}

// Support records userID's free "Support" tap on novelID — idempotent,
// tapping again while already supporting is a no-op. Deliberately not
// realtime-broadcast, same reasoning as views/ratings (see the plan's
// Phase 5d note): as frequent as either, and the small count shift
// isn't worth a refetch signal to every connected client.
func (novelService *NovelService) Support(ctx context.Context, novelID, userID string) (*repository.Novel, error) {
	if _, err := novelService.novels.GetByID(ctx, novelID); err != nil {
		return nil, err
	}
	if err := novelService.supports.Add(ctx, novelID, userID); err != nil {
		return nil, err
	}
	return novelService.Get(ctx, novelID)
}

// Unsupport withdraws userID's support of novelID.
func (novelService *NovelService) Unsupport(ctx context.Context, novelID, userID string) (*repository.Novel, error) {
	if _, err := novelService.novels.GetByID(ctx, novelID); err != nil {
		return nil, err
	}
	if err := novelService.supports.Remove(ctx, novelID, userID); err != nil {
		return nil, err
	}
	return novelService.Get(ctx, novelID)
}

// IsSupportedByUser is the logged-in caller's own support state for
// novelID, for the novel detail response's "my_support".
func (novelService *NovelService) IsSupportedByUser(ctx context.Context, novelID, userID string) (bool, error) {
	return novelService.supports.IsSupportedByUser(ctx, novelID, userID)
}

// attachGenres populates Genres on every novel in one batched query.
func (novelService *NovelService) attachGenres(ctx context.Context, novels []*repository.Novel) error {
	novelIDs := make([]string, len(novels))
	for index, novel := range novels {
		novelIDs[index] = novel.ID
	}
	byNovel, err := novelService.genres.ListForNovels(ctx, novelIDs)
	if err != nil {
		return err
	}
	for _, novel := range novels {
		novel.Genres = byNovel[novel.ID]
	}
	return nil
}

func (novelService *NovelService) UpdateCover(ctx context.Context, novelID, coverURL string) error {
	if err := novelService.novels.UpdateCoverURL(ctx, novelID, coverURL); err != nil {
		return err
	}
	novelService.events.Publish(realtime.Event{Topic: "novel.updated", ID: novelID})
	return nil
}

// Reorder sets each given novel's sort_order exactly as provided —
// see NovelRepository.Reorder for why positions are absolute rather
// than array indices.
func (novelService *NovelService) Reorder(ctx context.Context, positions []repository.NovelPosition) error {
	if len(positions) == 0 {
		return &ValidationError{Message: "positions is required"}
	}
	if err := novelService.novels.Reorder(ctx, positions); err != nil {
		return err
	}
	novelService.events.Publish(realtime.Event{Topic: "novel.updated"})
	return nil
}

func (novelService *NovelService) Delete(ctx context.Context, novelID string) error {
	if err := novelService.novels.SoftDelete(ctx, novelID); err != nil {
		return err
	}
	novelService.events.Publish(realtime.Event{Topic: "novel.deleted", ID: novelID})
	return nil
}

func validateNovelWrite(write *repository.NovelWrite) error {
	write.Title = strings.TrimSpace(write.Title)
	write.AuthorName = strings.TrimSpace(write.AuthorName)
	write.Synopsis = strings.TrimSpace(write.Synopsis)

	if write.Title == "" || len(write.Title) > maxNovelTitleLength {
		return &ValidationError{Message: fmt.Sprintf("title is required (max %d characters)", maxNovelTitleLength)}
	}
	if write.AuthorName == "" || len(write.AuthorName) > maxNovelAuthorLength {
		return &ValidationError{Message: fmt.Sprintf("author name is required (max %d characters)", maxNovelAuthorLength)}
	}
	if write.Status == "" {
		write.Status = "ongoing"
	}
	if write.Status != "ongoing" && write.Status != "completed" {
		return &ValidationError{Message: "status must be 'ongoing' or 'completed'"}
	}
	if write.Rating != nil && (*write.Rating < 0 || *write.Rating > 10) {
		return &ValidationError{Message: "rating must be between 0 and 10"}
	}
	if write.ViewCount < 0 {
		return &ValidationError{Message: "view count cannot be negative"}
	}
	return nil
}
