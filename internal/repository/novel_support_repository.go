package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type NovelSupportRepository struct {
	pool *pgxpool.Pool
}

func NewNovelSupportRepository(pool *pgxpool.Pool) *NovelSupportRepository {
	return &NovelSupportRepository{pool: pool}
}

const recomputeSupportCountSQL = `
	UPDATE novels SET support_count = (
		SELECT count(*) FROM novel_supports WHERE novel_id = $1
	) WHERE id = $1`

// Add records userID's support of novelID — idempotent, so tapping
// Support again while already supporting is a no-op rather than an
// error (ON CONFLICT DO NOTHING).
func (repository *NovelSupportRepository) Add(ctx context.Context, novelID, userID string) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin add support: %w", err)
	}
	defer transaction.Rollback(ctx)

	if _, err := transaction.Exec(ctx, `
		INSERT INTO novel_supports (novel_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (novel_id, user_id) DO NOTHING`,
		novelID, userID); err != nil {
		return fmt.Errorf("add support: %w", err)
	}
	if _, err := transaction.Exec(ctx, recomputeSupportCountSQL, novelID); err != nil {
		return fmt.Errorf("recompute support count: %w", err)
	}
	return transaction.Commit(ctx)
}

// Remove withdraws userID's support of novelID — idempotent, same shape as Add.
func (repository *NovelSupportRepository) Remove(ctx context.Context, novelID, userID string) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin remove support: %w", err)
	}
	defer transaction.Rollback(ctx)

	if _, err := transaction.Exec(ctx,
		"DELETE FROM novel_supports WHERE novel_id = $1 AND user_id = $2",
		novelID, userID); err != nil {
		return fmt.Errorf("remove support: %w", err)
	}
	if _, err := transaction.Exec(ctx, recomputeSupportCountSQL, novelID); err != nil {
		return fmt.Errorf("recompute support count: %w", err)
	}
	return transaction.Commit(ctx)
}

// IsSupportedByUser reports whether userID currently supports novelID —
// backs the novel detail response's "my_support".
func (repository *NovelSupportRepository) IsSupportedByUser(ctx context.Context, novelID, userID string) (bool, error) {
	var exists bool
	err := repository.pool.QueryRow(ctx,
		"SELECT EXISTS(SELECT 1 FROM novel_supports WHERE novel_id = $1 AND user_id = $2)",
		novelID, userID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check support: %w", err)
	}
	return exists, nil
}
