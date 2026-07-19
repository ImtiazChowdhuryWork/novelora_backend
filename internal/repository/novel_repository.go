package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNovelNotFound = errors.New("novel not found")

// Novel is a row in the novels table (soft-deleted rows are never returned).
type Novel struct {
	ID                string
	Title             string
	AuthorName        string
	Synopsis          string
	CoverURL          string
	Status            string // "ongoing" | "completed"
	IsShort           bool
	IsRecommended     bool
	Rating            *float64
	ViewCount         int64
	PublishedChapters int
	TotalChapters     int
	// SortOrder is the admin's manual display order (lower = earlier).
	// New novels are appended to the end; see Reorder for changing it.
	SortOrder int
	CreatedAt time.Time
	UpdatedAt time.Time
	// Genres is populated by NovelService, not this repository — see
	// GenreRepository.ListForNovel(s). Nil until attached.
	Genres []*Genre
}

// NovelListFilter narrows and pages the novels list.
type NovelListFilter struct {
	Search        string
	Status        string // "", "ongoing", "completed"
	IsShort       *bool  // nil = both
	IsRecommended *bool  // nil = both
	Page          int    // 1-based
	PageSize      int
}

// NovelWrite is the mutable subset used by Create and Update.
type NovelWrite struct {
	Title         string
	AuthorName    string
	Synopsis      string
	Status        string
	IsShort       bool
	IsRecommended bool
	Rating        *float64
}

type NovelRepository struct {
	pool *pgxpool.Pool
}

func NewNovelRepository(pool *pgxpool.Pool) *NovelRepository {
	return &NovelRepository{pool: pool}
}

const novelColumns = `
	n.id, n.title, n.author_name, n.synopsis, coalesce(n.cover_url, ''),
	n.status, n.is_short, n.is_recommended, n.rating, n.view_count,
	(SELECT count(*) FROM chapters c WHERE c.novel_id = n.id AND c.status = 'published'),
	(SELECT count(*) FROM chapters c WHERE c.novel_id = n.id),
	n.sort_order, n.created_at, n.updated_at`

func scanNovel(row pgx.Row) (*Novel, error) {
	novel := &Novel{}
	err := row.Scan(
		&novel.ID, &novel.Title, &novel.AuthorName, &novel.Synopsis, &novel.CoverURL,
		&novel.Status, &novel.IsShort, &novel.IsRecommended, &novel.Rating, &novel.ViewCount,
		&novel.PublishedChapters, &novel.TotalChapters,
		&novel.SortOrder, &novel.CreatedAt, &novel.UpdatedAt,
	)
	return novel, err
}

// List returns one page of novels plus the total row count for the filter.
func (repository *NovelRepository) List(ctx context.Context, filter NovelListFilter) ([]*Novel, int, error) {
	conditions := []string{"n.deleted_at IS NULL"}
	arguments := []any{}

	if filter.Search != "" {
		arguments = append(arguments, "%"+strings.ToLower(filter.Search)+"%")
		conditions = append(conditions, fmt.Sprintf(
			"(lower(n.title) LIKE $%d OR lower(n.author_name) LIKE $%d)", len(arguments), len(arguments)))
	}
	if filter.Status != "" {
		arguments = append(arguments, filter.Status)
		conditions = append(conditions, fmt.Sprintf("n.status = $%d", len(arguments)))
	}
	if filter.IsShort != nil {
		arguments = append(arguments, *filter.IsShort)
		conditions = append(conditions, fmt.Sprintf("n.is_short = $%d", len(arguments)))
	}
	if filter.IsRecommended != nil {
		arguments = append(arguments, *filter.IsRecommended)
		conditions = append(conditions, fmt.Sprintf("n.is_recommended = $%d", len(arguments)))
	}
	whereClause := strings.Join(conditions, " AND ")

	var total int
	err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM novels n WHERE "+whereClause, arguments...,
	).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("count novels: %w", err)
	}

	arguments = append(arguments, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := repository.pool.Query(ctx, fmt.Sprintf(
		"SELECT %s FROM novels n WHERE %s ORDER BY n.sort_order ASC, n.created_at DESC LIMIT $%d OFFSET $%d",
		novelColumns, whereClause, len(arguments)-1, len(arguments)), arguments...)
	if err != nil {
		return nil, 0, fmt.Errorf("list novels: %w", err)
	}
	defer rows.Close()

	novels := []*Novel{}
	for rows.Next() {
		novel, err := scanNovel(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan novel: %w", err)
		}
		novels = append(novels, novel)
	}
	return novels, total, rows.Err()
}

func (repository *NovelRepository) GetByID(ctx context.Context, novelID string) (*Novel, error) {
	novel, err := scanNovel(repository.pool.QueryRow(ctx,
		"SELECT "+novelColumns+" FROM novels n WHERE n.id = $1 AND n.deleted_at IS NULL",
		novelID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNovelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get novel: %w", err)
	}
	return novel, nil
}

// Create appends the new novel to the end of the manual display order.
func (repository *NovelRepository) Create(ctx context.Context, write NovelWrite) (*Novel, error) {
	novel, err := scanNovel(repository.pool.QueryRow(ctx, `
		WITH inserted AS (
			INSERT INTO novels (title, author_name, synopsis, status, is_short, is_recommended, rating, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7, (SELECT coalesce(max(sort_order), 0) + 1 FROM novels))
			RETURNING *
		)
		SELECT `+novelColumns+` FROM inserted n`,
		write.Title, write.AuthorName, write.Synopsis, write.Status,
		write.IsShort, write.IsRecommended, write.Rating))
	if err != nil {
		return nil, fmt.Errorf("insert novel: %w", err)
	}
	return novel, nil
}

// NovelPosition is one novel's new place in the manual display order.
type NovelPosition struct {
	NovelID   string
	SortOrder int
}

// Reorder sets sort_order = SortOrder for each given novel, exactly as
// provided — the caller (not this method) decides what those values
// mean. This matters because the dashboard's drag-to-reorder only ever
// has one page of novels loaded at a time: it computes each dragged
// novel's absolute sort_order (page offset + local position) rather
// than relying on array index, so reordering within page 2 can't
// collide with page 1's untouched sort_order values.
func (repository *NovelRepository) Reorder(ctx context.Context, positions []NovelPosition) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin reorder: %w", err)
	}
	defer transaction.Rollback(ctx)

	for _, position := range positions {
		if _, err := transaction.Exec(ctx,
			"UPDATE novels SET sort_order = $1 WHERE id = $2 AND deleted_at IS NULL",
			position.SortOrder, position.NovelID,
		); err != nil {
			return fmt.Errorf("reorder novel %s: %w", position.NovelID, err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit reorder: %w", err)
	}
	return nil
}

func (repository *NovelRepository) Update(ctx context.Context, novelID string, write NovelWrite) (*Novel, error) {
	novel, err := scanNovel(repository.pool.QueryRow(ctx, `
		WITH updated AS (
			UPDATE novels SET
				title = $2, author_name = $3, synopsis = $4, status = $5,
				is_short = $6, is_recommended = $7, rating = $8, updated_at = now()
			WHERE id = $1 AND deleted_at IS NULL
			RETURNING *
		)
		SELECT `+novelColumns+` FROM updated n`,
		novelID, write.Title, write.AuthorName, write.Synopsis, write.Status,
		write.IsShort, write.IsRecommended, write.Rating))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNovelNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("update novel: %w", err)
	}
	return novel, nil
}

// UpdateCoverURL replaces the cover; empty clears it.
func (repository *NovelRepository) UpdateCoverURL(ctx context.Context, novelID, coverURL string) error {
	commandTag, err := repository.pool.Exec(ctx,
		`UPDATE novels SET cover_url = nullif($2, ''), updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL`,
		novelID, coverURL)
	if err != nil {
		return fmt.Errorf("update novel cover: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNovelNotFound
	}
	return nil
}

// SoftDelete hides the novel (and its chapters via queries) without
// destroying data; recoverable by clearing deleted_at manually.
func (repository *NovelRepository) SoftDelete(ctx context.Context, novelID string) error {
	commandTag, err := repository.pool.Exec(ctx,
		"UPDATE novels SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL",
		novelID)
	if err != nil {
		return fmt.Errorf("soft delete novel: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrNovelNotFound
	}
	return nil
}
