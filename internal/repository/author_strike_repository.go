package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthorStrike is a persistent admin note against an author name — see
// the migration's comment on why this is name-keyed, not
// account-keyed. Never changes content by itself; that's
// NovelRepository.BulkHideByAuthorName's job, a separate action.
type AuthorStrike struct {
	ID            string
	AuthorName    string
	Note          string
	CreatedBy     *string
	CreatedByName string
	CreatedAt     time.Time
}

type AuthorStrikeRepository struct {
	pool *pgxpool.Pool
}

func NewAuthorStrikeRepository(pool *pgxpool.Pool) *AuthorStrikeRepository {
	return &AuthorStrikeRepository{pool: pool}
}

func (repository *AuthorStrikeRepository) Create(ctx context.Context, authorName, note, createdBy string) (*AuthorStrike, error) {
	var strikeID string
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO author_strikes (author_name, note, created_by)
		VALUES ($1, $2, $3) RETURNING id`,
		authorName, note, createdBy,
	).Scan(&strikeID)
	if err != nil {
		return nil, fmt.Errorf("create author strike: %w", err)
	}

	strike := &AuthorStrike{}
	err = repository.pool.QueryRow(ctx, `
		SELECT s.id, s.author_name, s.note, s.created_by, coalesce(u.username, ''), s.created_at
		FROM author_strikes s LEFT JOIN users u ON u.id = s.created_by
		WHERE s.id = $1`, strikeID,
	).Scan(&strike.ID, &strike.AuthorName, &strike.Note, &strike.CreatedBy, &strike.CreatedByName, &strike.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get created author strike: %w", err)
	}
	return strike, nil
}

// ListForAuthor returns every strike against an author name, newest
// first — shown on the admin report detail view's author panel.
func (repository *AuthorStrikeRepository) ListForAuthor(ctx context.Context, authorName string) ([]*AuthorStrike, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT s.id, s.author_name, s.note, s.created_by, coalesce(u.username, ''), s.created_at
		FROM author_strikes s LEFT JOIN users u ON u.id = s.created_by
		WHERE s.author_name = $1
		ORDER BY s.created_at DESC`, authorName)
	if err != nil {
		return nil, fmt.Errorf("list author strikes: %w", err)
	}
	defer rows.Close()

	strikes := []*AuthorStrike{}
	for rows.Next() {
		strike := &AuthorStrike{}
		if err := rows.Scan(&strike.ID, &strike.AuthorName, &strike.Note,
			&strike.CreatedBy, &strike.CreatedByName, &strike.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan author strike: %w", err)
		}
		strikes = append(strikes, strike)
	}
	return strikes, rows.Err()
}

// CountForAuthor is a cheap summary for list views that don't need the
// full strike history (e.g. a future author-list page).
func (repository *AuthorStrikeRepository) CountForAuthor(ctx context.Context, authorName string) (int, error) {
	var count int
	err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM author_strikes WHERE author_name = $1", authorName).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count author strikes: %w", err)
	}
	return count, nil
}
