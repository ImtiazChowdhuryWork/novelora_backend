package handler

import (
	"net/http"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

type AdminStatsHandler struct {
	stats *repository.StatsRepository
}

func NewAdminStatsHandler(stats *repository.StatsRepository) *AdminStatsHandler {
	return &AdminStatsHandler{stats: stats}
}

// Get: GET /admin/stats
func (adminStatsHandler *AdminStatsHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	stats, err := adminStatsHandler.stats.Get(request.Context())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"total_novels":       stats.TotalNovels,
		"total_users":        stats.TotalUsers,
		"total_chapters":     stats.TotalChapters,
		"published_chapters": stats.PublishedChapters,
		"total_genres":       stats.TotalGenres,
	})
}
