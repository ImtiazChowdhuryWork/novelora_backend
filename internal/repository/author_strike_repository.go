package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthorStrike is a persistent admin note against an author — either a
// real account (OwnerUserID, migration 0032) when the reported novel
// has one, or the free-text author name otherwise (see migration
// 0029's comment on why name-keyed was the only option at first).
// Never changes content by itself; that's NovelRepository's
// BulkHideByAuthorName/BulkHideByOwnerUserID's job, a separate action.
type AuthorStrike struct {
	ID            string
	AuthorName    string
	OwnerUserID   *string
	Note          string
	// Severity is one of 'minor', 'moderate', 'severe' (migration
	// 0042) — feeds the Profile tab's computed risk level instead of
	// every strike counting the same regardless of what it was for.
	Severity      string
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

const strikeColumns = `
	s.id, s.author_name, s.owner_user_id, s.note, s.severity, s.created_by, coalesce(u.username, ''), s.created_at`

const strikeFromClause = `author_strikes s LEFT JOIN users u ON u.id = s.created_by`

func scanStrike(row pgx.Row) (*AuthorStrike, error) {
	strike := &AuthorStrike{}
	err := row.Scan(&strike.ID, &strike.AuthorName, &strike.OwnerUserID, &strike.Note, &strike.Severity,
		&strike.CreatedBy, &strike.CreatedByName, &strike.CreatedAt)
	return strike, err
}

// Create records a strike against an author name and, when the
// reported novel has a real account (ownerUserID non-nil), stamps
// that too — see migration 0032. severity is one of 'minor',
// 'moderate', 'severe' — validated by the caller (service layer),
// enforced again by the DB check constraint (migration 0042).
func (repository *AuthorStrikeRepository) Create(ctx context.Context, authorName string, ownerUserID *string, note, severity, createdBy string) (*AuthorStrike, error) {
	var strikeID string
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO author_strikes (author_name, owner_user_id, note, severity, created_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		authorName, ownerUserID, note, severity, createdBy,
	).Scan(&strikeID)
	if err != nil {
		return nil, fmt.Errorf("create author strike: %w", err)
	}

	strike, err := scanStrike(repository.pool.QueryRow(ctx,
		"SELECT "+strikeColumns+" FROM "+strikeFromClause+" WHERE s.id = $1", strikeID))
	if err != nil {
		return nil, fmt.Errorf("get created author strike: %w", err)
	}
	return strike, nil
}

// ListForAuthor returns every strike against an author name, newest
// first — shown on the admin report detail view's author panel, for
// novels with no real owner account (the name-only fallback path).
func (repository *AuthorStrikeRepository) ListForAuthor(ctx context.Context, authorName string) ([]*AuthorStrike, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT "+strikeColumns+" FROM "+strikeFromClause+" WHERE s.author_name = $1 ORDER BY s.created_at DESC",
		authorName)
	if err != nil {
		return nil, fmt.Errorf("list author strikes: %w", err)
	}
	defer rows.Close()

	strikes := []*AuthorStrike{}
	for rows.Next() {
		strike, err := scanStrike(rows)
		if err != nil {
			return nil, fmt.Errorf("scan author strike: %w", err)
		}
		strikes = append(strikes, strike)
	}
	return strikes, rows.Err()
}

// ListForOwner is ListForAuthor's real-account counterpart — used
// instead of it when the reported novel has an owner_user_id.
func (repository *AuthorStrikeRepository) ListForOwner(ctx context.Context, ownerUserID string) ([]*AuthorStrike, error) {
	rows, err := repository.pool.Query(ctx,
		"SELECT "+strikeColumns+" FROM "+strikeFromClause+" WHERE s.owner_user_id = $1 ORDER BY s.created_at DESC",
		ownerUserID)
	if err != nil {
		return nil, fmt.Errorf("list author strikes by owner: %w", err)
	}
	defer rows.Close()

	strikes := []*AuthorStrike{}
	for rows.Next() {
		strike, err := scanStrike(rows)
		if err != nil {
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

// CountForOwner is CountForAuthor's real-account counterpart.
func (repository *AuthorStrikeRepository) CountForOwner(ctx context.Context, ownerUserID string) (int, error) {
	var count int
	err := repository.pool.QueryRow(ctx,
		"SELECT count(*) FROM author_strikes WHERE owner_user_id = $1", ownerUserID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count author strikes by owner: %w", err)
	}
	return count, nil
}
