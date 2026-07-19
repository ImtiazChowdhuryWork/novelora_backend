package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrChapterNotFound = errors.New("chapter not found")

// Chapter is a row in the chapters table. ContentJSON is the editor's
// structured document; ContentText is its plain-text mirror.
type Chapter struct {
	ID          string
	NovelID     string
	Number      int
	Title       string
	ContentJSON string
	ContentText string
	WordCount   int
	Status      string // "draft" | "published"
	PublishedAt *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ChapterWrite is the mutable content subset.
type ChapterWrite struct {
	Title       string
	ContentJSON string
	ContentText string
	WordCount   int
}

type ChapterRepository struct {
	pool *pgxpool.Pool
}

func NewChapterRepository(pool *pgxpool.Pool) *ChapterRepository {
	return &ChapterRepository{pool: pool}
}

// ListByNovel returns chapters without their content — list payloads
// stay lean; content is fetched per chapter. onlyPublished narrows to
// what readers may see.
func (repository *ChapterRepository) ListByNovel(ctx context.Context, novelID string, onlyPublished bool) ([]*Chapter, error) {
	statusCondition := ""
	if onlyPublished {
		statusCondition = " AND status = 'published'"
	}
	rows, err := repository.pool.Query(ctx, `
		SELECT id, novel_id, number, title, word_count, status, published_at, created_at, updated_at
		FROM chapters WHERE novel_id = $1`+statusCondition+` ORDER BY number`, novelID)
	if err != nil {
		return nil, fmt.Errorf("list chapters: %w", err)
	}
	defer rows.Close()

	chapters := []*Chapter{}
	for rows.Next() {
		chapter := &Chapter{}
		if err := rows.Scan(
			&chapter.ID, &chapter.NovelID, &chapter.Number, &chapter.Title,
			&chapter.WordCount, &chapter.Status, &chapter.PublishedAt,
			&chapter.CreatedAt, &chapter.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan chapter: %w", err)
		}
		chapters = append(chapters, chapter)
	}
	return chapters, rows.Err()
}

func (repository *ChapterRepository) GetByID(ctx context.Context, chapterID string) (*Chapter, error) {
	chapter := &Chapter{}
	err := repository.pool.QueryRow(ctx, `
		SELECT id, novel_id, number, title, content_json, content_text, word_count,
		       status, published_at, created_at, updated_at
		FROM chapters WHERE id = $1`, chapterID,
	).Scan(
		&chapter.ID, &chapter.NovelID, &chapter.Number, &chapter.Title,
		&chapter.ContentJSON, &chapter.ContentText, &chapter.WordCount,
		&chapter.Status, &chapter.PublishedAt, &chapter.CreatedAt, &chapter.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrChapterNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get chapter: %w", err)
	}
	return chapter, nil
}

// CreateBatch appends chapters as drafts, numbering after the novel's
// current maximum — all inside one transaction (an import is atomic).
func (repository *ChapterRepository) CreateBatch(ctx context.Context, novelID string, writes []ChapterWrite) (int, error) {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin import transaction: %w", err)
	}
	defer transaction.Rollback(ctx)

	var nextNumber int
	err = transaction.QueryRow(ctx,
		"SELECT coalesce(max(number), 0) + 1 FROM chapters WHERE novel_id = $1", novelID,
	).Scan(&nextNumber)
	if err != nil {
		return 0, fmt.Errorf("next chapter number: %w", err)
	}

	for offset, write := range writes {
		_, err := transaction.Exec(ctx, `
			INSERT INTO chapters (novel_id, number, title, content_json, content_text, word_count)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			novelID, nextNumber+offset, write.Title,
			write.ContentJSON, write.ContentText, write.WordCount)
		if err != nil {
			return 0, fmt.Errorf("insert chapter %d: %w", nextNumber+offset, err)
		}
	}

	if err := transaction.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit import: %w", err)
	}
	return len(writes), nil
}

func (repository *ChapterRepository) Update(ctx context.Context, chapterID string, write ChapterWrite) (*Chapter, error) {
	commandTag, err := repository.pool.Exec(ctx, `
		UPDATE chapters SET title = $2, content_json = $3, content_text = $4,
		       word_count = $5, updated_at = now()
		WHERE id = $1`,
		chapterID, write.Title, write.ContentJSON, write.ContentText, write.WordCount)
	if err != nil {
		return nil, fmt.Errorf("update chapter: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return nil, ErrChapterNotFound
	}
	return repository.GetByID(ctx, chapterID)
}

// UpdateStatus publishes or unpublishes; published_at tracks the change.
func (repository *ChapterRepository) UpdateStatus(ctx context.Context, chapterID, status string) (*Chapter, error) {
	commandTag, err := repository.pool.Exec(ctx, `
		UPDATE chapters SET status = $2,
		       published_at = CASE WHEN $2 = 'published' THEN now() ELSE NULL END,
		       updated_at = now()
		WHERE id = $1`, chapterID, status)
	if err != nil {
		return nil, fmt.Errorf("update chapter status: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return nil, ErrChapterNotFound
	}
	return repository.GetByID(ctx, chapterID)
}

func (repository *ChapterRepository) Delete(ctx context.Context, chapterID string) error {
	commandTag, err := repository.pool.Exec(ctx,
		"DELETE FROM chapters WHERE id = $1", chapterID)
	if err != nil {
		return fmt.Errorf("delete chapter: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrChapterNotFound
	}
	return nil
}
