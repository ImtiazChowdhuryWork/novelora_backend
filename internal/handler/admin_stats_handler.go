package handler

import (
	"net/http"
	"strconv"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const (
	defaultSeriesDays = 30
	maxSeriesDays     = 180
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

type dailyStatResponse struct {
	Date              string `json:"date"`
	Signups           int    `json:"signups"`
	NovelsCreated     int    `json:"novels_created"`
	ChaptersPublished int    `json:"chapters_published"`
}

// Series: GET /admin/stats/series?days=30
func (adminStatsHandler *AdminStatsHandler) Series(responseWriter http.ResponseWriter, request *http.Request) {
	days, _ := strconv.Atoi(request.URL.Query().Get("days"))
	if days < 1 {
		days = defaultSeriesDays
	}
	if days > maxSeriesDays {
		days = maxSeriesDays
	}

	series, err := adminStatsHandler.stats.DailySeries(request.Context(), days)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	items := make([]dailyStatResponse, 0, len(series))
	for _, stat := range series {
		items = append(items, dailyStatResponse{
			Date:              stat.Date.Format("2006-01-02"),
			Signups:           stat.Signups,
			NovelsCreated:     stat.NovelsCreated,
			ChaptersPublished: stat.ChaptersPublished,
		})
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}
