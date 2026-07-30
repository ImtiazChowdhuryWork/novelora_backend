package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NovelRating is one reader's vote on one novel — a 1-5 star rating.
type NovelRating struct {
	NovelID   string
	UserID    string
	Username  string // populated only by ListForNovel's join
	Rating    int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type NovelRatingRepository struct {
	pool *pgxpool.Pool
}

func NewNovelRatingRepository(pool *pgxpool.Pool) *NovelRatingRepository {
	return &NovelRatingRepository{pool: pool}
}

// recomputeAggregate is run inside the same transaction as every write
// below, so novels.average_rating/rating_count never drift from the
// actual novel_ratings rows. average_rating is stored on the admin
// rating's 0-10 scale (star vote * 2) — see the migration's doc comment.
const recomputeAggregateSQL = `
	UPDATE novels SET
		average_rating = (SELECT round(avg(rating)::numeric * 2, 1) FROM novel_ratings WHERE novel_id = $1),
		rating_count = (SELECT count(*) FROM novel_ratings WHERE novel_id = $1)
	WHERE id = $1`

// Upsert records userID's rating of novelID (1-5), creating or
// overwriting their previous vote, and recomputes the novel's aggregate
// in the same transaction.
func (repository *NovelRatingRepository) Upsert(ctx context.Context, novelID, userID string, rating int) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin upsert rating: %w", err)
	}
	defer transaction.Rollback(ctx)

	if _, err := transaction.Exec(ctx, `
		INSERT INTO novel_ratings (novel_id, user_id, rating, created_at, updated_at)
		VALUES ($1, $2, $3, now(), now())
		ON CONFLICT (novel_id, user_id) DO UPDATE SET rating = $3, updated_at = now()`,
		novelID, userID, rating); err != nil {
		return fmt.Errorf("upsert rating: %w", err)
	}
	if _, err := transaction.Exec(ctx, recomputeAggregateSQL, novelID); err != nil {
		return fmt.Errorf("recompute rating aggregate: %w", err)
	}
	return transaction.Commit(ctx)
}

// Remove deletes userID's rating of novelID, if any, and recomputes the
// novel's aggregate in the same transaction. Not an error if the user
// never rated this novel — same idempotent shape as removing an
// override (see NovelSectionOverrideRepository.Remove).
func (repository *NovelRatingRepository) Remove(ctx context.Context, novelID, userID string) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin remove rating: %w", err)
	}
	defer transaction.Rollback(ctx)

	if _, err := transaction.Exec(ctx,
		"DELETE FROM novel_ratings WHERE novel_id = $1 AND user_id = $2",
		novelID, userID); err != nil {
		return fmt.Errorf("remove rating: %w", err)
	}
	if _, err := transaction.Exec(ctx, recomputeAggregateSQL, novelID); err != nil {
		return fmt.Errorf("recompute rating aggregate: %w", err)
	}
	return transaction.Commit(ctx)
}

// GetForUser returns userID's own rating of novelID, or nil if they
// haven't rated it — backs the novel detail response's "my_rating".
func (repository *NovelRatingRepository) GetForUser(ctx context.Context, novelID, userID string) (*int, error) {
	var rating int
	err := repository.pool.QueryRow(ctx,
		"SELECT rating FROM novel_ratings WHERE novel_id = $1 AND user_id = $2",
		novelID, userID,
	).Scan(&rating)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rating for user: %w", err)
	}
	return &rating, nil
}

// ListForNovel returns every individual rating on novelID, most recent
// first, with the rater's username joined in — the admin moderation
// view backing a single-rating delete (see the plan's Phase 5b note on
// not hand-editing ratings, just removing abusive/spam ones).
func (repository *NovelRatingRepository) ListForNovel(ctx context.Context, novelID string) ([]*NovelRating, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT r.novel_id, r.user_id, u.username, r.rating, r.created_at, r.updated_at
		FROM novel_ratings r
		JOIN users u ON u.id = r.user_id
		WHERE r.novel_id = $1
		ORDER BY r.created_at DESC`, novelID)
	if err != nil {
		return nil, fmt.Errorf("list ratings for novel: %w", err)
	}
	defer rows.Close()

	ratings := []*NovelRating{}
	for rows.Next() {
		rating := &NovelRating{}
		if err := rows.Scan(
			&rating.NovelID, &rating.UserID, &rating.Username,
			&rating.Rating, &rating.CreatedAt, &rating.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan rating: %w", err)
		}
		ratings = append(ratings, rating)
	}
	return ratings, rows.Err()
}
