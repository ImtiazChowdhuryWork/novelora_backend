package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrGenreNotFound  = errors.New("genre not found")
	ErrGenreNameTaken = errors.New("genre name already exists")
)

type Genre struct {
	ID   string
	Name string
}

type GenreRepository struct {
	pool *pgxpool.Pool
}

func NewGenreRepository(pool *pgxpool.Pool) *GenreRepository {
	return &GenreRepository{pool: pool}
}

func (repository *GenreRepository) List(ctx context.Context) ([]*Genre, error) {
	rows, err := repository.pool.Query(ctx, "SELECT id, name FROM genres ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("list genres: %w", err)
	}
	defer rows.Close()

	genres := []*Genre{}
	for rows.Next() {
		genre := &Genre{}
		if err := rows.Scan(&genre.ID, &genre.Name); err != nil {
			return nil, fmt.Errorf("scan genre: %w", err)
		}
		genres = append(genres, genre)
	}
	return genres, rows.Err()
}

func (repository *GenreRepository) Create(ctx context.Context, name string) (*Genre, error) {
	genre := &Genre{}
	err := repository.pool.QueryRow(ctx,
		"INSERT INTO genres (name) VALUES ($1) RETURNING id, name", name,
	).Scan(&genre.ID, &genre.Name)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == pgerrcodeUniqueViolation {
			return nil, ErrGenreNameTaken
		}
		return nil, fmt.Errorf("insert genre: %w", err)
	}
	return genre, nil
}

func (repository *GenreRepository) Delete(ctx context.Context, genreID string) error {
	commandTag, err := repository.pool.Exec(ctx, "DELETE FROM genres WHERE id = $1", genreID)
	if err != nil {
		return fmt.Errorf("delete genre: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return ErrGenreNotFound
	}
	return nil
}

// ListForNovel returns the genres assigned to one novel.
func (repository *GenreRepository) ListForNovel(ctx context.Context, novelID string) ([]*Genre, error) {
	byNovel, err := repository.ListForNovels(ctx, []string{novelID})
	if err != nil {
		return nil, err
	}
	return byNovel[novelID], nil
}

// ListForNovels batches the same lookup across a page of novels (avoids
// an N+1 query when the admin/public novel list renders genre tags).
func (repository *GenreRepository) ListForNovels(ctx context.Context, novelIDs []string) (map[string][]*Genre, error) {
	result := map[string][]*Genre{}
	if len(novelIDs) == 0 {
		return result, nil
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT ng.novel_id, g.id, g.name
		FROM novel_genres ng
		JOIN genres g ON g.id = ng.genre_id
		WHERE ng.novel_id = ANY($1)
		ORDER BY g.name`, novelIDs)
	if err != nil {
		return nil, fmt.Errorf("list genres for novels: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var novelID string
		genre := &Genre{}
		if err := rows.Scan(&novelID, &genre.ID, &genre.Name); err != nil {
			return nil, fmt.Errorf("scan novel genre: %w", err)
		}
		result[novelID] = append(result[novelID], genre)
	}
	return result, rows.Err()
}

// SetForNovel replaces a novel's genre assignments wholesale — simpler
// and cheap enough at this scale than diffing the existing set.
func (repository *GenreRepository) SetForNovel(ctx context.Context, novelID string, genreIDs []string) error {
	transaction, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin set genres: %w", err)
	}
	defer transaction.Rollback(ctx)

	if _, err := transaction.Exec(ctx, "DELETE FROM novel_genres WHERE novel_id = $1", novelID); err != nil {
		return fmt.Errorf("clear novel genres: %w", err)
	}
	for _, genreID := range genreIDs {
		if _, err := transaction.Exec(ctx,
			"INSERT INTO novel_genres (novel_id, genre_id) VALUES ($1, $2)",
			novelID, genreID,
		); err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == pgerrcodeForeignKeyViolation {
				return ErrGenreNotFound
			}
			return fmt.Errorf("assign novel genre: %w", err)
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("commit set genres: %w", err)
	}
	return nil
}

const pgerrcodeForeignKeyViolation = "23503"
