package repository

import (
	"context"
	"fmt"
	"time"

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
	PendingReports    int
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
			(SELECT count(*) FROM genres),
			(SELECT count(*) FROM novel_reports WHERE status NOT IN ('resolved', 'rejected'))`,
	).Scan(&stats.TotalNovels, &stats.TotalUsers, &stats.TotalChapters,
		&stats.PublishedChapters, &stats.TotalGenres, &stats.PendingReports)
	if err != nil {
		return nil, fmt.Errorf("get stats: %w", err)
	}
	return stats, nil
}

// DailyStat is one day's worth of activity — zero-filled for days with
// no activity so a chart's x-axis stays continuous.
type DailyStat struct {
	Date              time.Time
	Signups           int
	NovelsCreated     int
	ChaptersPublished int
}

// DailySeries returns one row per day for the trailing `days` days
// (including today), derivable from timestamps the schema already
// has — no separate event-tracking table needed. View counts aren't
// included here: view_count is a running total with no per-event
// timestamp, so a real "views over time" trend would need new
// instrumentation, not just a new query.
func (repository *StatsRepository) DailySeries(ctx context.Context, days int) ([]*DailyStat, error) {
	rows, err := repository.pool.Query(ctx, `
		WITH days AS (
			SELECT generate_series(current_date - ($1::int - 1), current_date, interval '1 day')::date AS day
		),
		signups AS (
			SELECT date(created_at) AS day, count(*) AS count FROM users GROUP BY 1
		),
		novels_created AS (
			SELECT date(created_at) AS day, count(*) AS count FROM novels WHERE deleted_at IS NULL GROUP BY 1
		),
		chapters_published AS (
			SELECT date(published_at) AS day, count(*) AS count FROM chapters WHERE published_at IS NOT NULL GROUP BY 1
		)
		SELECT days.day, coalesce(signups.count, 0), coalesce(novels_created.count, 0), coalesce(chapters_published.count, 0)
		FROM days
		LEFT JOIN signups ON signups.day = days.day
		LEFT JOIN novels_created ON novels_created.day = days.day
		LEFT JOIN chapters_published ON chapters_published.day = days.day
		ORDER BY days.day`, days)
	if err != nil {
		return nil, fmt.Errorf("daily stats series: %w", err)
	}
	defer rows.Close()

	series := []*DailyStat{}
	for rows.Next() {
		stat := &DailyStat{}
		if err := rows.Scan(&stat.Date, &stat.Signups, &stat.NovelsCreated, &stat.ChaptersPublished); err != nil {
			return nil, fmt.Errorf("scan daily stat: %w", err)
		}
		series = append(series, stat)
	}
	return series, rows.Err()
}
