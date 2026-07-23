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
	novels        *repository.NovelRepository
	genres        *repository.GenreRepository
	deviceTokens  *repository.DeviceTokenRepository
	notifications *repository.NotificationRepository
	notifier      push.Notifier
	events        realtime.Publisher
}

func NewNovelService(
	novels *repository.NovelRepository,
	genres *repository.GenreRepository,
	deviceTokens *repository.DeviceTokenRepository,
	notifications *repository.NotificationRepository,
	notifier push.Notifier,
	events realtime.Publisher,
) *NovelService {
	return &NovelService{
		novels:        novels,
		genres:        genres,
		deviceTokens:  deviceTokens,
		notifications: notifications,
		notifier:      notifier,
		events:        events,
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
	return nil
}
