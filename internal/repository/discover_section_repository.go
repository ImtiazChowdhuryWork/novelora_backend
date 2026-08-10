package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDiscoverSectionNotFound = errors.New("discover section not found")

// DiscoverSection is one declarative row describing a Discover section's
// data source — the single source of truth this replaces the two
// hand-duplicated lists with (DiscoverCubit's Dart constants and
// RankingNotificationService's defaultRankingSections). See migration
// 0019 for the full field-by-field rationale.
type DiscoverSection struct {
	Key                 string
	Category            string
	Label               string
	Layout              string // "ranked_grid" | "shelf" | "picks_feed"
	Scope               string // "genre" | "global" | "multi_genre"
	GenreNames          []string
	Sort                string // "" | "trending" | "views" | "rating" | "new"
	StatusFilter        string // "" | "ongoing" | "completed"
	RecommendedFilter   *bool
	ExclusiveFilter     *bool
	ExcludeSectionKeys  []string
	Position            int
	Active              bool
	Personalize         bool
}

// DiscoverSectionWrite is the mutable subset used by Create and Update.
type DiscoverSectionWrite struct {
	Category           string
	Label              string
	Layout             string
	Scope              string
	GenreNames         []string
	Sort               string
	StatusFilter       string
	RecommendedFilter  *bool
	ExclusiveFilter    *bool
	ExcludeSectionKeys []string
	Position           int
	Active             bool
	Personalize        bool
}

type DiscoverSectionRepository struct {
	pool *pgxpool.Pool
}

func NewDiscoverSectionRepository(pool *pgxpool.Pool) *DiscoverSectionRepository {
	return &DiscoverSectionRepository{pool: pool}
}

const discoverSectionColumns = `
	key, category, label, layout, scope, genre_names, sort, status_filter,
	recommended_filter, exclusive_filter, exclude_section_keys, position, active, personalize`

func scanDiscoverSection(row pgx.Row) (*DiscoverSection, error) {
	section := &DiscoverSection{}
	err := row.Scan(
		&section.Key, &section.Category, &section.Label, &section.Layout, &section.Scope,
		&section.GenreNames, &section.Sort, &section.StatusFilter,
		&section.RecommendedFilter, &section.ExclusiveFilter, &section.ExcludeSectionKeys,
		&section.Position, &section.Active, &section.Personalize,
	)
	return section, err
}

// List returns every section (active and inactive — the dashboard needs
// both) ordered for stable category-grouped display. Callers that only
// want what the app should render filter on Active themselves (see
// NovelService-style filtering, or ListActive below).
func (repository *DiscoverSectionRepository) List(ctx context.Context) ([]*DiscoverSection, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT "+discoverSectionColumns+" FROM discover_sections ORDER BY category, position")
	if err != nil {
		return nil, fmt.Errorf("list discover sections: %w", err)
	}
	defer rows.Close()

	sections := []*DiscoverSection{}
	for rows.Next() {
		section, err := scanDiscoverSection(rows)
		if err != nil {
			return nil, fmt.Errorf("scan discover section: %w", err)
		}
		sections = append(sections, section)
	}
	return sections, rows.Err()
}

// ListActive is what the app fetches — same ordering, active only.
func (repository *DiscoverSectionRepository) ListActive(ctx context.Context) ([]*DiscoverSection, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT "+discoverSectionColumns+" FROM discover_sections WHERE active ORDER BY category, position")
	if err != nil {
		return nil, fmt.Errorf("list active discover sections: %w", err)
	}
	defer rows.Close()

	sections := []*DiscoverSection{}
	for rows.Next() {
		section, err := scanDiscoverSection(rows)
		if err != nil {
			return nil, fmt.Errorf("scan discover section: %w", err)
		}
		sections = append(sections, section)
	}
	return sections, rows.Err()
}

func (repository *DiscoverSectionRepository) Get(ctx context.Context, key string) (*DiscoverSection, error) {
	section, err := scanDiscoverSection(repository.pool.QueryRow(ctx,
		"SELECT "+discoverSectionColumns+" FROM discover_sections WHERE key = $1", key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDiscoverSectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get discover section: %w", err)
	}
	return section, nil
}

// Create inserts a new section under a caller-chosen key (unlike Novel's
// generated uuid — a section key is a stable, human-legible identifier
// referenced by exclude_section_keys and novel_section_overrides, so it
// has to be caller-controlled and immutable, not surrogate).
func (repository *DiscoverSectionRepository) Create(ctx context.Context, key string, write DiscoverSectionWrite) (*DiscoverSection, error) {
	section, err := scanDiscoverSection(repository.pool.QueryRow(ctx, `
		INSERT INTO discover_sections
			(key, category, label, layout, scope, genre_names, sort, status_filter,
			 recommended_filter, exclusive_filter, exclude_section_keys, position, active, personalize)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING `+discoverSectionColumns,
		key, write.Category, write.Label, write.Layout, write.Scope, write.GenreNames,
		write.Sort, write.StatusFilter, write.RecommendedFilter, write.ExclusiveFilter,
		write.ExcludeSectionKeys, write.Position, write.Active, write.Personalize))
	if err != nil {
		return nil, fmt.Errorf("insert discover section: %w", err)
	}
	return section, nil
}

func (repository *DiscoverSectionRepository) Update(ctx context.Context, key string, write DiscoverSectionWrite) (*DiscoverSection, error) {
	section, err := scanDiscoverSection(repository.pool.QueryRow(ctx, `
		UPDATE discover_sections SET
			category = $2, label = $3, layout = $4, scope = $5, genre_names = $6,
			sort = $7, status_filter = $8, recommended_filter = $9, exclusive_filter = $10,
			exclude_section_keys = $11, position = $12, active = $13, personalize = $14, updated_at = now()
		WHERE key = $1
		RETURNING `+discoverSectionColumns,
		key, write.Category, write.Label, write.Layout, write.Scope, write.GenreNames,
		write.Sort, write.StatusFilter, write.RecommendedFilter, write.ExclusiveFilter,
		write.ExcludeSectionKeys, write.Position, write.Active, write.Personalize))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDiscoverSectionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update discover section: %w", err)
	}
	return section, nil
}

func (repository *DiscoverSectionRepository) Delete(ctx context.Context, key string) error {
	commandTag, err := repository.pool.Exec(ctx, "DELETE FROM discover_sections WHERE key = $1", key)
	if err != nil {
		return fmt.Errorf("delete discover section: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrDiscoverSectionNotFound
	}
	return nil
}

// NovelSectionOverride is an admin's manual pin/exclude for one novel in
// one section — the escape hatch on top of every section's algorithmic
// fill (see migration 0020).
type NovelSectionOverride struct {
	SectionKey string
	NovelID    string
	Type       string // "pinned" | "excluded"
	Position   *int
}

type NovelSectionOverrideRepository struct {
	pool *pgxpool.Pool
}

func NewNovelSectionOverrideRepository(pool *pgxpool.Pool) *NovelSectionOverrideRepository {
	return &NovelSectionOverrideRepository{pool: pool}
}

// ListForSection returns every override for one section — pinned entries
// first (ordered by Position, nulls last), then excluded.
func (repository *NovelSectionOverrideRepository) ListForSection(ctx context.Context, sectionKey string) ([]*NovelSectionOverride, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT section_key, novel_id, type, position FROM novel_section_overrides
		WHERE section_key = $1
		ORDER BY type, position NULLS LAST`, sectionKey)
	if err != nil {
		return nil, fmt.Errorf("list section overrides: %w", err)
	}
	defer rows.Close()

	overrides := []*NovelSectionOverride{}
	for rows.Next() {
		override := &NovelSectionOverride{}
		if err := rows.Scan(&override.SectionKey, &override.NovelID, &override.Type, &override.Position); err != nil {
			return nil, fmt.Errorf("scan section override: %w", err)
		}
		overrides = append(overrides, override)
	}
	return overrides, rows.Err()
}

// ListForNovel returns every section a novel has a manual override in —
// backs the Phase 4 dashboard advisor's "pinned/excluded" indicators.
func (repository *NovelSectionOverrideRepository) ListForNovel(ctx context.Context, novelID string) ([]*NovelSectionOverride, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT section_key, novel_id, type, position FROM novel_section_overrides
		WHERE novel_id = $1
		ORDER BY section_key`, novelID)
	if err != nil {
		return nil, fmt.Errorf("list novel overrides: %w", err)
	}
	defer rows.Close()

	overrides := []*NovelSectionOverride{}
	for rows.Next() {
		override := &NovelSectionOverride{}
		if err := rows.Scan(&override.SectionKey, &override.NovelID, &override.Type, &override.Position); err != nil {
			return nil, fmt.Errorf("scan section override: %w", err)
		}
		overrides = append(overrides, override)
	}
	return overrides, rows.Err()
}

// Set upserts one novel's override for one section — a novel can only be
// pinned OR excluded in a given section (the primary key is
// section_key+novel_id), so re-setting replaces whichever it was before.
func (repository *NovelSectionOverrideRepository) Set(ctx context.Context, sectionKey, novelID, overrideType string, position *int) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO novel_section_overrides (section_key, novel_id, type, position)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (section_key, novel_id) DO UPDATE SET type = $3, position = $4`,
		sectionKey, novelID, overrideType, position)
	if err != nil {
		return fmt.Errorf("set section override: %w", err)
	}
	return nil
}

func (repository *NovelSectionOverrideRepository) Remove(ctx context.Context, sectionKey, novelID string) error {
	_, err := repository.pool.Exec(ctx,
		"DELETE FROM novel_section_overrides WHERE section_key = $1 AND novel_id = $2",
		sectionKey, novelID)
	if err != nil {
		return fmt.Errorf("remove section override: %w", err)
	}
	return nil
}
