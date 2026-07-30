package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ReadingHistoryEntry is one (user, novel) row: where a reader last
// left off, without the full novel record — see
// ReadingHistoryService.ListForUser for resolving these into novels.
type ReadingHistoryEntry struct {
	NovelID       string
	LastChapterID *string
	LastReadAt    time.Time
}

type ReadingHistoryRepository struct {
	pool *pgxpool.Pool
}

func NewReadingHistoryRepository(pool *pgxpool.Pool) *ReadingHistoryRepository {
	return &ReadingHistoryRepository{pool: pool}
}

// Upsert records that userID just read chapterID of novelID, bumping
// last_read_at to now — called on every chapter open, so this is the
// hot path and stays a single statement.
func (repository *ReadingHistoryRepository) Upsert(ctx context.Context, userID, novelID, chapterID string) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO reading_history (user_id, novel_id, last_chapter_id, last_read_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, novel_id) DO UPDATE
			SET last_chapter_id = $3, last_read_at = now()`,
		userID, novelID, chapterID)
	if err != nil {
		return fmt.Errorf("upsert reading history: %w", err)
	}
	return nil
}

// ListForUser returns userID's reading history, most recently read
// novel first.
func (repository *ReadingHistoryRepository) ListForUser(ctx context.Context, userID string, limit int) ([]*ReadingHistoryEntry, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT novel_id, last_chapter_id, last_read_at
		FROM reading_history
		WHERE user_id = $1
		ORDER BY last_read_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("list reading history: %w", err)
	}
	defer rows.Close()

	entries := []*ReadingHistoryEntry{}
	for rows.Next() {
		entry := &ReadingHistoryEntry{}
		if err := rows.Scan(&entry.NovelID, &entry.LastChapterID, &entry.LastReadAt); err != nil {
			return nil, fmt.Errorf("scan reading history entry: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// TopGenreIDs returns userID's most-read genre/tag ids, most frequent
// first — Phase 5e's personalization signal, computed fresh on every
// call (not cached/broadcast; see ReadingHistoryService's doc comment
// on why this stays pull-based). Empty for a user with no history yet,
// so callers naturally fall back to their own unpersonalized default.
func (repository *ReadingHistoryRepository) TopGenreIDs(ctx context.Context, userID string, limit int) ([]string, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT ng.genre_id
		FROM reading_history rh
		JOIN novel_genres ng ON ng.novel_id = rh.novel_id
		WHERE rh.user_id = $1
		GROUP BY ng.genre_id
		ORDER BY count(*) DESC, ng.genre_id
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("top genre ids: %w", err)
	}
	defer rows.Close()

	genreIDs := []string{}
	for rows.Next() {
		var genreID string
		if err := rows.Scan(&genreID); err != nil {
			return nil, fmt.Errorf("scan top genre id: %w", err)
		}
		genreIDs = append(genreIDs, genreID)
	}
	return genreIDs, rows.Err()
}
