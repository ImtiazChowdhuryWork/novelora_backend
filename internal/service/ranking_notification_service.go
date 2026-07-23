package service

import (
	"context"
	"fmt"
	"log"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/push"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// RankingSection is one ranked/curated Discover section worth notifying
// readers about when a novel newly enters it. GenreName "" means the
// section is global (unfiltered, e.g. Trending); otherwise it's looked
// up by name on every run — same known caveat as the app's own
// genre-name lookups (see DiscoverCubit.categoryMostReadGenres): a
// genre renamed in the admin Genres page silently stops matching until
// this list is updated too.
type RankingSection struct {
	Key       string
	Label     string
	GenreName string
	Sort      string
	Limit     int
}

// defaultRankingSections is deliberately small to start: Trending
// (global) and Comedy's own Most Read — the one dedicated category
// section built so far. Add one entry per future category's ranked
// section (Werewolf's Most Read, Mafia's Editor's Pick, ...); no new
// code is needed beyond this list, the same extensibility shape as the
// "every category" realtime requirement.
var defaultRankingSections = []RankingSection{
	{Key: "trending", Label: "Trending", Sort: "views", Limit: 9},
	{Key: "comedy_most_read", Label: "Comedy's Most Read", GenreName: "Comedy", Sort: "views", Limit: 9},
}

// RankingNotificationService periodically checks whether any novel has
// newly entered one of the tracked ranked sections and notifies every
// reader about it — see DetectAndNotify.
type RankingNotificationService struct {
	novels        *repository.NovelRepository
	genres        *repository.GenreRepository
	memberships   *repository.SectionMembershipRepository
	notifications *repository.NotificationRepository
	deviceTokens  *repository.DeviceTokenRepository
	notifier      push.Notifier
	events        realtime.Publisher
	sections      []RankingSection
}

func NewRankingNotificationService(
	novels *repository.NovelRepository,
	genres *repository.GenreRepository,
	memberships *repository.SectionMembershipRepository,
	notifications *repository.NotificationRepository,
	deviceTokens *repository.DeviceTokenRepository,
	notifier push.Notifier,
	events realtime.Publisher,
) *RankingNotificationService {
	return &RankingNotificationService{
		novels:        novels,
		genres:        genres,
		memberships:   memberships,
		notifications: notifications,
		deviceTokens:  deviceTokens,
		notifier:      notifier,
		events:        events,
		sections:      defaultRankingSections,
	}
}

// DetectAndNotify re-runs every registered section's query, syncs which
// novels are in it now (see SectionMembershipRepository.Sync), and
// notifies readers about any novel that's newly present since the last
// check — never for one that was already there. Failures on one
// section are logged and skipped rather than aborting the rest.
func (rankingService *RankingNotificationService) DetectAndNotify(ctx context.Context) {
	genres, err := rankingService.genres.List(ctx)
	if err != nil {
		log.Printf("ranking: list genres: %v", err)
		return
	}

	for _, section := range rankingService.sections {
		genreID := ""
		if section.GenreName != "" {
			var match *repository.Genre
			for _, genre := range genres {
				if genre.Name == section.GenreName {
					match = genre
					break
				}
			}
			if match == nil {
				continue
			}
			genreID = match.ID
		}

		novels, _, err := rankingService.novels.List(ctx, repository.NovelListFilter{
			GenreID:  genreID,
			Sort:     section.Sort,
			Page:     1,
			PageSize: section.Limit,
		})
		if err != nil {
			log.Printf("ranking: list %s: %v", section.Key, err)
			continue
		}

		currentIDs := make([]string, len(novels))
		byID := make(map[string]*repository.Novel, len(novels))
		for index, novel := range novels {
			currentIDs[index] = novel.ID
			byID[novel.ID] = novel
		}

		newIDs, err := rankingService.memberships.Sync(ctx, section.Key, currentIDs)
		if err != nil {
			log.Printf("ranking: sync %s: %v", section.Key, err)
			continue
		}
		if len(newIDs) == 0 {
			continue
		}

		newNovels := make([]*repository.Novel, len(newIDs))
		for index, novelID := range newIDs {
			newNovels[index] = byID[novelID]
		}
		rankingService.notifyEnteredSection(ctx, newNovels, section)
	}
}

// notifyEnteredSection sends one push per check per section, not one per
// novel — a cold start (or just a busy check) can surface several new
// entries at once, and a burst of separate pushes for the same section
// reads as spam rather than several distinct events. A lone entry still
// deep-links straight to that novel; multiple entries fall back to a
// summary push with no specific destination (NotifyBroadcast — a tap
// just opens the inbox). The in-app inbox is unaffected either way: it
// always gets one row per novel, so the full detail survives even when
// the push itself is a digest.
func (rankingService *RankingNotificationService) notifyEnteredSection(ctx context.Context, novels []*repository.Novel, section RankingSection) {
	tokens, err := rankingService.deviceTokens.ListAllTokens(ctx)
	if err != nil {
		log.Printf("push: could not load device tokens: %v", err)
	} else if len(novels) == 1 {
		body := fmt.Sprintf("%q just entered %s", novels[0].Title, section.Label)
		rankingService.notifier.NotifyNovelHighlight(ctx, tokens, novels[0].ID, novels[0].Title, body)
	} else {
		title := fmt.Sprintf("New in %s", section.Label)
		rankingService.notifier.NotifyBroadcast(ctx, tokens, title, digestSummary(novels))
	}

	for _, novel := range novels {
		body := fmt.Sprintf("%q just entered %s", novel.Title, section.Label)
		if err := rankingService.notifications.CreateNovelNotificationForAllUsers(ctx, novel.ID, novel.Title, body); err != nil {
			log.Printf("inbox: could not create ranking notification for novel %s: %v", novel.ID, err)
		}
	}
	rankingService.events.Publish(realtime.Event{Topic: "notification.new"})
}

// digestSummary names the first couple of novels and counts the rest —
// same shape as a typical "Alice and 2 others liked this" digest.
// Only called with len(novels) >= 2 (see notifyEnteredSection).
func digestSummary(novels []*repository.Novel) string {
	if len(novels) == 2 {
		return fmt.Sprintf("%q and %q", novels[0].Title, novels[1].Title)
	}
	return fmt.Sprintf("%q, %q, and %d more", novels[0].Title, novels[1].Title, len(novels)-2)
}
