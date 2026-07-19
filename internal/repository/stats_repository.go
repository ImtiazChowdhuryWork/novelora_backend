package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Stats backs the dashboard's Overview page — a handful of aggregate
// counts, cheap enough to compute on every request at this scale.
type Stats struct {
	TotalNovels       int
	TotalUsers        int
	TotalChapters     int
	PublishedChapters int
	TotalGenres       int
}

type StatsRepository struct {
	pool *pgxpool.Pool
}

func NewStatsRepository(pool *pgxpool.Pool) *StatsRepository {
	return &StatsRepository{pool: pool}
}

func (repository *StatsRepository) Get(ctx context.Context) (*Stats, error) {
	stats := &Stats{}
	err := repository.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM novels WHERE deleted_at IS NULL),
			(SELECT count(*) FROM users),
			(SELECT count(*) FROM chapters),
			(SELECT count(*) FROM chapters WHERE status = 'published'),
			(SELECT count(*) FROM genres)`,
	).Scan(&stats.TotalNovels, &stats.TotalUsers, &stats.TotalChapters,
		&stats.PublishedChapters, &stats.TotalGenres)
	if err != nil {
		return nil, fmt.Errorf("get stats: %w", err)
	}
	return stats, nil
}
