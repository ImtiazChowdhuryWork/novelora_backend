package service

import (
	"context"
	"fmt"
	"log"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/push"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// rankingCheckLimit is how many of a section's top novels are checked
// for new entrants each run — matches the shelf-preview sizes every
// section already renders at (8-12), not the full catalog.
const rankingCheckLimit = 9

// RankingNotificationService periodically checks whether any novel has
// newly entered one of the active Section Registry rows (discover_sections)
// and notifies every reader about it — see DetectAndNotify. Previously
// drove this off a small hardcoded Go list (defaultRankingSections) that
// had to be kept in sync by hand with DiscoverCubit's own Dart constants;
// both collapsed into discover_sections (see migration 0019 and
// DiscoverSectionService), so this now covers every active section with
// no code changes when one is added, renamed, or removed from the
// dashboard.
type RankingNotificationService struct {
	discoverSections *DiscoverSectionService
	notifications    *repository.NotificationRepository
	deviceTokens     *repository.DeviceTokenRepository
	memberships      *repository.SectionMembershipRepository
	notifier         push.Notifier
	events           realtime.Publisher
}

func NewRankingNotificationService(
	discoverSections *DiscoverSectionService,
	memberships *repository.SectionMembershipRepository,
	notifications *repository.NotificationRepository,
	deviceTokens *repository.DeviceTokenRepository,
	notifier push.Notifier,
	events realtime.Publisher,
) *RankingNotificationService {
	return &RankingNotificationService{
		discoverSections: discoverSections,
		memberships:      memberships,
		notifications:    notifications,
		deviceTokens:     deviceTokens,
		notifier:         notifier,
		events:           events,
	}
}

// DetectAndNotify re-resolves every active discover_sections row, syncs
// which novels are in it now (see SectionMembershipRepository.Sync), and
// notifies readers about any novel that's newly present since the last
// check — never for one that was already there. Failures on one section
// are logged and skipped rather than aborting the rest.
func (rankingService *RankingNotificationService) DetectAndNotify(ctx context.Context) {
	sections, err := rankingService.discoverSections.ListActive(ctx)
	if err != nil {
		log.Printf("ranking: list active sections: %v", err)
		return
	}

	for _, section := range sections {
		novels, err := rankingService.discoverSections.Resolve(ctx, section.Key, rankingCheckLimit)
		if err != nil {
			log.Printf("ranking: resolve %s: %v", section.Key, err)
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
		rankingService.notifyEnteredSection(ctx, newNovels, section.Label)
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
func (rankingService *RankingNotificationService) notifyEnteredSection(ctx context.Context, novels []*repository.Novel, sectionLabel string) {
	tokens, err := rankingService.deviceTokens.ListAllTokens(ctx)
	if err != nil {
		log.Printf("push: could not load device tokens: %v", err)
	} else if len(novels) == 1 {
		body := fmt.Sprintf("%q just entered %s", novels[0].Title, sectionLabel)
		rankingService.notifier.NotifyNovelHighlight(ctx, tokens, novels[0].ID, novels[0].Title, body)
	} else {
		title := fmt.Sprintf("New in %s", sectionLabel)
		rankingService.notifier.NotifyBroadcast(ctx, tokens, title, digestSummary(novels))
	}

	for _, novel := range novels {
		body := fmt.Sprintf("%q just entered %s", novel.Title, sectionLabel)
		if err := rankingService.notifications.CreateNovelNotificationForAllUsers(ctx, novel.ID, novel.Title, body); err != nil {
			log.Printf("inbox: could not create ranking notification for novel %s: %v", novel.ID, err)
		}
	}
	rankingService.events.Publish(realtime.Event{Topic: "notification.new"})
	// Also nudge Discover itself, not just the inbox/badge — without
	// this, a ranked section reordering here (e.g. from the trending
	// score decaying over time, no admin action involved) would only
	// ever show up in the app after some other unrelated novel/chapter
	// edit happened to trigger a refetch. Reuses "novel.updated" with no
	// ID (a plain broadcast, unlike BookDetailCubit's own ID-filtered use
	// of this same topic) purely as a "go refetch" signal every Discover
	// listener already reacts to via libraryRealtimeTopics.
	rankingService.events.Publish(realtime.Event{Topic: "novel.updated"})
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
