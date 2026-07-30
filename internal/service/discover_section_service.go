package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// maxExcludeDepth bounds how many levels of exclude_section_keys get
// resolved when computing what a section excludes — an admin-configured
// chain (Romance's "Exclusive" excludes "Editor's Picks", which itself
// excludes "Love On Top") is expected and handled; an accidental cycle
// (A excludes B excludes A) just stops here instead of looping forever.
const maxExcludeDepth = 4

// sectionResolveFanout bounds how many novels are pulled per genre when
// resolving a multi_genre section's algorithmic fill, before merging —
// generous enough that dedup/exclusion doesn't starve the final list
// short of the caller's requested limit in ordinary catalog sizes.
const sectionResolveFanout = 50

// excludeSiblingLimit bounds how many novels are pulled from a sibling
// section when computing exclude_section_keys — deliberately a shelf-
// preview-sized number (matching what NovelShelf/RankedGridShelf
// actually render, e.g. Romance's "Love On Top"), not the full
// genre-scoped catalog. Using sectionResolveFanout here instead would
// exclude far more than the sibling section actually shows on screen,
// and in a small catalog (e.g. 40 Romance novels) can wrongly exclude
// the entire genre pool, leaving nothing left to fill this section.
const excludeSiblingLimit = 12

type DiscoverSectionService struct {
	sections  *repository.DiscoverSectionRepository
	overrides *repository.NovelSectionOverrideRepository
	novels    *repository.NovelRepository
	genres    *repository.GenreRepository
	events    realtime.Publisher
}

func NewDiscoverSectionService(
	sections *repository.DiscoverSectionRepository,
	overrides *repository.NovelSectionOverrideRepository,
	novels *repository.NovelRepository,
	genres *repository.GenreRepository,
	events realtime.Publisher,
) *DiscoverSectionService {
	return &DiscoverSectionService{
		sections:  sections,
		overrides: overrides,
		novels:    novels,
		genres:    genres,
		events:    events,
	}
}

func (service *DiscoverSectionService) List(ctx context.Context) ([]*repository.DiscoverSection, error) {
	return service.sections.List(ctx)
}

func (service *DiscoverSectionService) ListActive(ctx context.Context) ([]*repository.DiscoverSection, error) {
	return service.sections.ListActive(ctx)
}

func (service *DiscoverSectionService) Get(ctx context.Context, key string) (*repository.DiscoverSection, error) {
	return service.sections.Get(ctx, key)
}

func validateSectionWrite(write *repository.DiscoverSectionWrite) error {
	if write.Category == "" {
		return &ValidationError{Message: "category is required"}
	}
	if write.Label == "" {
		return &ValidationError{Message: "label is required"}
	}
	switch write.Layout {
	case "ranked_grid", "shelf", "picks_feed":
	default:
		return &ValidationError{Message: "layout must be 'ranked_grid', 'shelf', or 'picks_feed'"}
	}
	switch write.Scope {
	case "genre", "global", "multi_genre":
	default:
		return &ValidationError{Message: "scope must be 'genre', 'global', or 'multi_genre'"}
	}
	if (write.Scope == "genre" || write.Scope == "multi_genre") && len(write.GenreNames) == 0 {
		return &ValidationError{Message: "genre_names is required for genre/multi_genre scope"}
	}
	switch write.Sort {
	case "", "trending", "views", "rating", "new":
	default:
		return &ValidationError{Message: "sort must be '', 'trending', 'views', 'rating', or 'new'"}
	}
	switch write.StatusFilter {
	case "", "ongoing", "completed":
	default:
		return &ValidationError{Message: "status_filter must be '', 'ongoing', or 'completed'"}
	}
	return nil
}

func (service *DiscoverSectionService) Create(ctx context.Context, key string, write repository.DiscoverSectionWrite) (*repository.DiscoverSection, error) {
	if key == "" {
		return nil, &ValidationError{Message: "key is required"}
	}
	if err := validateSectionWrite(&write); err != nil {
		return nil, err
	}
	section, err := service.sections.Create(ctx, key, write)
	if err != nil {
		return nil, err
	}
	service.events.Publish(realtime.Event{Topic: "discover_section.updated"})
	return section, nil
}

func (service *DiscoverSectionService) Update(ctx context.Context, key string, write repository.DiscoverSectionWrite) (*repository.DiscoverSection, error) {
	if err := validateSectionWrite(&write); err != nil {
		return nil, err
	}
	section, err := service.sections.Update(ctx, key, write)
	if err != nil {
		return nil, err
	}
	service.events.Publish(realtime.Event{Topic: "discover_section.updated"})
	return section, nil
}

func (service *DiscoverSectionService) Delete(ctx context.Context, key string) error {
	if err := service.sections.Delete(ctx, key); err != nil {
		return err
	}
	service.events.Publish(realtime.Event{Topic: "discover_section.updated"})
	return nil
}

func (service *DiscoverSectionService) ListOverrides(ctx context.Context, sectionKey string) ([]*repository.NovelSectionOverride, error) {
	return service.overrides.ListForSection(ctx, sectionKey)
}

// ListOverridesForNovel returns every section a novel currently has a
// manual pin/exclude override in — backs the dashboard's per-novel
// "Discover Sections" panel (see Suggest for the algorithmic half of
// that same panel).
func (service *DiscoverSectionService) ListOverridesForNovel(ctx context.Context, novelID string) ([]*repository.NovelSectionOverride, error) {
	return service.overrides.ListForNovel(ctx, novelID)
}

// SuggestionCriteria is a novel's shape as far as section matching
// cares — deliberately not a full repository.Novel, since this also
// needs to work against an in-progress, not-yet-saved novel in the
// dashboard's create form (no id yet).
type SuggestionCriteria struct {
	GenreNames    []string
	Status        string
	IsRecommended bool
	IsExclusive   bool
}

// SectionSuggestion is one section a novel currently qualifies for
// algorithmically, plus a human-readable reason — e.g. "genre: Fantasy,
// recommended".
type SectionSuggestion struct {
	Key      string
	Category string
	Label    string
	Reason   string
}

// Suggest evaluates criteria against every active section's own
// scope/genre_names/filter — the exact same rules Resolve's
// algorithmicFill uses to populate a section, just run once against a
// single novel instead of listing the whole catalog. Pure, read-only
// evaluation: never touches overrides (an admin pins/excludes
// separately, deliberately independent of what the algorithm would
// decide) and needs no ML/scoring, since section membership was always
// just a rule to begin with.
func (service *DiscoverSectionService) Suggest(ctx context.Context, criteria SuggestionCriteria) ([]SectionSuggestion, error) {
	sections, err := service.sections.ListActive(ctx)
	if err != nil {
		return nil, err
	}

	genreSet := make(map[string]bool, len(criteria.GenreNames))
	for _, name := range criteria.GenreNames {
		genreSet[name] = true
	}

	suggestions := []SectionSuggestion{}
	for _, section := range sections {
		matchedGenre, genreMatches := matchesGenreScope(section, genreSet)
		if !genreMatches {
			continue
		}
		if section.StatusFilter != "" && section.StatusFilter != criteria.Status {
			continue
		}
		if section.RecommendedFilter != nil && *section.RecommendedFilter && !criteria.IsRecommended {
			continue
		}
		if section.ExclusiveFilter != nil && *section.ExclusiveFilter && !criteria.IsExclusive {
			continue
		}

		reasons := []string{}
		if matchedGenre != "" {
			reasons = append(reasons, "genre: "+matchedGenre)
		}
		if section.StatusFilter != "" {
			reasons = append(reasons, "status: "+section.StatusFilter)
		}
		if section.RecommendedFilter != nil && *section.RecommendedFilter {
			reasons = append(reasons, "recommended")
		}
		if section.ExclusiveFilter != nil && *section.ExclusiveFilter {
			reasons = append(reasons, "exclusive")
		}
		if len(reasons) == 0 {
			reasons = append(reasons, "global — no filter")
		}

		suggestions = append(suggestions, SectionSuggestion{
			Key:      section.Key,
			Category: section.Category,
			Label:    section.Label,
			Reason:   strings.Join(reasons, ", "),
		})
	}
	return suggestions, nil
}

// matchesGenreScope reports whether genreSet satisfies section's scope,
// and which genre name is the reason why (empty for "global", which has
// no genre condition to name).
func matchesGenreScope(section *repository.DiscoverSection, genreSet map[string]bool) (matchedGenre string, matches bool) {
	switch section.Scope {
	case "global":
		return "", true
	case "genre":
		if len(section.GenreNames) == 0 {
			return "", false
		}
		name := section.GenreNames[0]
		return name, genreSet[name]
	case "multi_genre":
		for _, name := range section.GenreNames {
			if genreSet[name] {
				return name, true
			}
		}
		return "", false
	default:
		return "", false
	}
}

// SetOverride pins or excludes one novel in one section — the admin
// escape hatch on top of every section's algorithmic fill. overrideType
// must be "pinned" or "excluded"; position only matters for "pinned"
// (nil pins to the front).
func (service *DiscoverSectionService) SetOverride(ctx context.Context, sectionKey, novelID, overrideType string, position *int) error {
	if overrideType != "pinned" && overrideType != "excluded" {
		return &ValidationError{Message: "type must be 'pinned' or 'excluded'"}
	}
	if _, err := service.sections.Get(ctx, sectionKey); err != nil {
		return err
	}
	if _, err := service.novels.GetByID(ctx, novelID); err != nil {
		return err
	}
	if err := service.overrides.Set(ctx, sectionKey, novelID, overrideType, position); err != nil {
		return err
	}
	service.events.Publish(realtime.Event{Topic: "novel_section_override.updated"})
	return nil
}

func (service *DiscoverSectionService) RemoveOverride(ctx context.Context, sectionKey, novelID string) error {
	if err := service.overrides.Remove(ctx, sectionKey, novelID); err != nil {
		return err
	}
	service.events.Publish(realtime.Event{Topic: "novel_section_override.updated"})
	return nil
}

// Resolve computes a section's actual novel list: algorithmic fill (per
// its scope/genre_names/sort/filter) → drop excluded overrides and
// exclude_section_keys → splice in pinned overrides (at Position if set,
// else the front) → trim to limit, deduped throughout. This is the one
// place section membership is actually computed — RankingNotificationService
// uses it directly (see its DetectAndNotify), and it's the eventual
// backing for any app-facing "give me this section's novels" endpoint.
func (service *DiscoverSectionService) Resolve(ctx context.Context, key string, limit int) ([]*repository.Novel, error) {
	section, err := service.sections.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	novels, err := service.resolveSection(ctx, section, limit, maxExcludeDepth)
	if err != nil {
		return nil, err
	}
	// NovelRepository.List/ListByIDs deliberately don't populate Genres
	// (see Novel's own doc comment — that's NovelService's job); attached
	// once here, on the final result only, not on every internal
	// algorithmic/exclude-resolution fetch along the way, since those
	// never need it themselves.
	if err := attachGenres(ctx, service.genres, novels); err != nil {
		return nil, fmt.Errorf("attach genres for %s: %w", key, err)
	}
	return novels, nil
}

func (service *DiscoverSectionService) resolveSection(ctx context.Context, section *repository.DiscoverSection, limit, excludeDepth int) ([]*repository.Novel, error) {
	base, err := service.algorithmicFill(ctx, section, limit)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", section.Key, err)
	}

	excludedIDs, err := service.resolveExcludedIDs(ctx, section.ExcludeSectionKeys, excludeDepth)
	if err != nil {
		return nil, err
	}
	overrides, err := service.overrides.ListForSection(ctx, section.Key)
	if err != nil {
		return nil, fmt.Errorf("list overrides for %s: %w", section.Key, err)
	}

	pinnedIDs := make([]string, 0, len(overrides))
	for _, override := range overrides {
		switch override.Type {
		case "excluded":
			excludedIDs[override.NovelID] = true
		case "pinned":
			pinnedIDs = append(pinnedIDs, override.NovelID)
		}
	}

	filtered := make([]*repository.Novel, 0, len(base))
	for _, novel := range base {
		if !excludedIDs[novel.ID] {
			filtered = append(filtered, novel)
		}
	}

	if len(pinnedIDs) == 0 {
		return trimNovels(filtered, limit), nil
	}

	pinnedNovels, err := service.novels.ListByIDs(ctx, pinnedIDs)
	if err != nil {
		return nil, fmt.Errorf("load pinned novels for %s: %w", section.Key, err)
	}
	pinnedByID := make(map[string]*repository.Novel, len(pinnedNovels))
	for _, novel := range pinnedNovels {
		pinnedByID[novel.ID] = novel
	}

	seen := make(map[string]bool, len(filtered)+len(pinnedIDs))
	result := make([]*repository.Novel, 0, len(filtered)+len(pinnedIDs))
	for _, novel := range pinnedNovels {
		if seen[novel.ID] {
			continue
		}
		seen[novel.ID] = true
		result = append(result, novel)
	}
	for _, novel := range filtered {
		if seen[novel.ID] || pinnedByID[novel.ID] != nil {
			continue
		}
		seen[novel.ID] = true
		result = append(result, novel)
	}
	return trimNovels(result, limit), nil
}

func trimNovels(novels []*repository.Novel, limit int) []*repository.Novel {
	if limit > 0 && len(novels) > limit {
		return novels[:limit]
	}
	return novels
}

// resolveExcludedIDs computes the union of every novel id currently in
// each of the given sections — deliberately their algorithmic fill only
// (own excludes/overrides applied, but not descending into further
// exclude_section_keys beyond excludeDepth), matching how the app's own
// exclusion chains already work (e.g. Romance's "Exclusive" excluding
// "Editor's Picks", which itself already excludes "Love On Top").
func (service *DiscoverSectionService) resolveExcludedIDs(ctx context.Context, keys []string, excludeDepth int) (map[string]bool, error) {
	excludedIDs := map[string]bool{}
	if excludeDepth <= 0 {
		return excludedIDs, nil
	}
	for _, key := range keys {
		section, err := service.sections.Get(ctx, key)
		if err != nil {
			continue // a renamed/deleted section key silently contributes nothing
		}
		novels, err := service.resolveSection(ctx, section, excludeSiblingLimit, excludeDepth-1)
		if err != nil {
			return nil, err
		}
		for _, novel := range novels {
			excludedIDs[novel.ID] = true
		}
	}
	return excludedIDs, nil
}

// algorithmicFill runs the section's own scope/genre_names/sort/filter —
// the same three shapes every Discover section has always used (genre,
// global, multi_genre merge), just driven by data instead of Dart
// constants. No overrides applied here — resolveSection layers those on
// top.
func (service *DiscoverSectionService) algorithmicFill(ctx context.Context, section *repository.DiscoverSection, limit int) ([]*repository.Novel, error) {
	baseFilter := repository.NovelListFilter{
		Sort:     section.Sort,
		Status:   section.StatusFilter,
		Page:     1,
		PageSize: limit,
	}
	if section.RecommendedFilter != nil {
		baseFilter.IsRecommended = section.RecommendedFilter
	}
	if section.ExclusiveFilter != nil {
		baseFilter.IsExclusive = section.ExclusiveFilter
	}

	switch section.Scope {
	case "global":
		novels, _, err := service.novels.List(ctx, baseFilter)
		return novels, err

	case "genre":
		if len(section.GenreNames) == 0 {
			return []*repository.Novel{}, nil
		}
		genreID, err := service.genreIDByName(ctx, section.GenreNames[0])
		if err != nil {
			return nil, err
		}
		if genreID == "" {
			return []*repository.Novel{}, nil // renamed/deleted genre — silently nothing, same as elsewhere
		}
		filter := baseFilter
		filter.GenreID = genreID
		novels, _, err := service.novels.List(ctx, filter)
		return novels, err

	case "multi_genre":
		allGenres, err := service.genres.List(ctx)
		if err != nil {
			return nil, err
		}
		byName := make(map[string]string, len(allGenres))
		for _, genre := range allGenres {
			byName[genre.Name] = genre.ID
		}

		merged := make([]*repository.Novel, 0, limit)
		seen := make(map[string]bool, limit)
		fanoutFilter := baseFilter
		fanoutFilter.PageSize = sectionResolveFanout
		for _, name := range section.GenreNames {
			genreID, ok := byName[name]
			if !ok {
				continue // not in the taxonomy yet — silently skipped, same fallback as every other genre lookup
			}
			perGenreFilter := fanoutFilter
			perGenreFilter.GenreID = genreID
			novels, _, err := service.novels.List(ctx, perGenreFilter)
			if err != nil {
				continue // one genre's failed request contributes nothing, doesn't fail the whole section
			}
			for _, novel := range novels {
				if seen[novel.ID] {
					continue
				}
				seen[novel.ID] = true
				merged = append(merged, novel)
			}
		}
		return merged, nil

	default:
		return []*repository.Novel{}, nil
	}
}

func (service *DiscoverSectionService) genreIDByName(ctx context.Context, name string) (string, error) {
	allGenres, err := service.genres.List(ctx)
	if err != nil {
		return "", err
	}
	for _, genre := range allGenres {
		if genre.Name == name {
			return genre.ID, nil
		}
	}
	return "", nil
}
