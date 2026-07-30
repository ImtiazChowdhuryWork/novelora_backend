package service

import (
	"context"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// attachGenres populates each novel's Genres field. NovelRepository's
// own fetches (List/ListByIDs) deliberately don't do this — it's a
// batched follow-up query, meant to run once on a final result set,
// not on every internal fetch along the way that never needs it.
func attachGenres(ctx context.Context, genres *repository.GenreRepository, novels []*repository.Novel) error {
	novelIDs := make([]string, len(novels))
	for index, novel := range novels {
		novelIDs[index] = novel.ID
	}
	byNovel, err := genres.ListForNovels(ctx, novelIDs)
	if err != nil {
		return err
	}
	for _, novel := range novels {
		novel.Genres = byNovel[novel.ID]
	}
	return nil
}
