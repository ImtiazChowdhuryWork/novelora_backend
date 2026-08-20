package handler

import (
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

type AdminNovelHandler struct {
	novelService    *service.NovelService
	auditLogger     *audit.Logger
	coversDirectory string
	coversUrlPrefix string
}

func NewAdminNovelHandler(novelService *service.NovelService, auditLogger *audit.Logger, uploadsDirectory string) *AdminNovelHandler {
	return &AdminNovelHandler{
		novelService:    novelService,
		auditLogger:     auditLogger,
		coversDirectory: filepath.Join(uploadsDirectory, "covers"),
		coversUrlPrefix: "/uploads/covers/",
	}
}

type novelWriteRequest struct {
	Title         string   `json:"title"`
	AuthorName    string   `json:"author_name"`
	Synopsis      string   `json:"synopsis"`
	Status        string   `json:"status"`
	IsShort       bool     `json:"is_short"`
	IsRecommended bool     `json:"is_recommended"`
	IsExclusive   bool     `json:"is_exclusive"`
	Rating        *float64 `json:"rating"`
	ViewCount     int64    `json:"view_count"`
	GenreIDs      []string `json:"genre_ids"`
}

func (writeRequest novelWriteRequest) toWrite() repository.NovelWrite {
	return repository.NovelWrite{
		Title:         writeRequest.Title,
		AuthorName:    writeRequest.AuthorName,
		Synopsis:      writeRequest.Synopsis,
		Status:        writeRequest.Status,
		IsShort:       writeRequest.IsShort,
		IsRecommended: writeRequest.IsRecommended,
		IsExclusive:   writeRequest.IsExclusive,
		Rating:        writeRequest.Rating,
		ViewCount:     writeRequest.ViewCount,
	}
}

type genreResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type novelResponse struct {
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	AuthorName        string          `json:"author_name"`
	Synopsis          string          `json:"synopsis"`
	CoverURL          string          `json:"cover_url"`
	Status            string          `json:"status"`
	IsShort           bool            `json:"is_short"`
	IsRecommended     bool            `json:"is_recommended"`
	IsExclusive       bool            `json:"is_exclusive"`
	Rating            *float64        `json:"rating"`
	AverageRating     *float64        `json:"average_rating"`
	RatingCount       int             `json:"rating_count"`
	SupportCount      int             `json:"support_count"`
	ViewCount         int64           `json:"view_count"`
	PublishedChapters int             `json:"published_chapters"`
	TotalChapters     int             `json:"total_chapters"`
	Genres            []genreResponse `json:"genres"`
	SortOrder         int             `json:"sort_order"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	// Hidden is only ever true on the author-scoped list (see
	// AuthorNovelHandler.List's IncludeHidden) — every other caller never
	// gets a hidden novel back in the first place, so this is always
	// false there.
	Hidden bool `json:"hidden"`
	// CommentCount is only ever populated by AuthorModerationHandler.
	// OtherNovels (the report detail drawer's Published tab) — every
	// other caller leaves it at its zero value, since no other list
	// screen shows it.
	CommentCount int `json:"comment_count"`
}

func newNovelResponse(novel *repository.Novel) novelResponse {
	genres := make([]genreResponse, 0, len(novel.Genres))
	for _, genre := range novel.Genres {
		genres = append(genres, genreResponse{ID: genre.ID, Name: genre.Name, Kind: genre.Kind})
	}
	return novelResponse{
		ID:                novel.ID,
		Title:             novel.Title,
		AuthorName:        novel.AuthorName,
		Synopsis:          novel.Synopsis,
		CoverURL:          novel.CoverURL,
		Status:            novel.Status,
		IsShort:           novel.IsShort,
		IsRecommended:     novel.IsRecommended,
		IsExclusive:       novel.IsExclusive,
		Rating:            novel.Rating,
		AverageRating:     novel.AverageRating,
		RatingCount:       novel.RatingCount,
		SupportCount:      novel.SupportCount,
		ViewCount:         novel.ViewCount,
		PublishedChapters: novel.PublishedChapters,
		TotalChapters:     novel.TotalChapters,
		Genres:            genres,
		SortOrder:         novel.SortOrder,
		CreatedAt:         novel.CreatedAt,
		UpdatedAt:         novel.UpdatedAt,
		Hidden:            novel.Hidden,
	}
}

// List returns a page of novels: GET /admin/novels?page=&page_size=&search=&status=&sort=&genre_ids=&genre_match=
// genre_ids is a comma-separated list of genre/tag ids; genre_match is
// "any" (default, OR) or "all" (AND) across that list.
func (adminNovelHandler *AdminNovelHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	var genreIDs []string
	if raw := query.Get("genre_ids"); raw != "" {
		for _, id := range strings.Split(raw, ",") {
			if id != "" {
				genreIDs = append(genreIDs, id)
			}
		}
	}

	filter := repository.NovelListFilter{
		Search:         query.Get("search"),
		Status:         query.Get("status"),
		Sort:           query.Get("sort"),
		GenreIDs:       genreIDs,
		GenreMatchMode: query.Get("genre_match"),
		Page:           page,
		PageSize:       pageSize,
	}

	novels, total, err := adminNovelHandler.novelService.List(request.Context(), filter)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	items := make([]novelResponse, 0, len(novels))
	for _, novel := range novels {
		items = append(items, newNovelResponse(novel))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"items": items,
		"total": total,
	})
}

func (adminNovelHandler *AdminNovelHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	novel, err := adminNovelHandler.novelService.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}

func (adminNovelHandler *AdminNovelHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest novelWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	novel, err := adminNovelHandler.novelService.Create(request.Context(), writeRequest.toWrite(), writeRequest.GenreIDs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	adminNovelHandler.auditLogger.Log(actorID, actorName, "novel.created", "novel", novel.ID,
		map[string]any{"title": novel.Title})
	writeJSON(responseWriter, http.StatusCreated, newNovelResponse(novel))
}

func (adminNovelHandler *AdminNovelHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest novelWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	novel, err := adminNovelHandler.novelService.Update(
		request.Context(), request.PathValue("id"), writeRequest.toWrite(), writeRequest.GenreIDs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	adminNovelHandler.auditLogger.Log(actorID, actorName, "novel.updated", "novel", novel.ID,
		map[string]any{"title": novel.Title})
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}

func (adminNovelHandler *AdminNovelHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	novelID := request.PathValue("id")
	novel, err := adminNovelHandler.novelService.Get(request.Context(), novelID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	if err := adminNovelHandler.novelService.Delete(request.Context(), novelID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	adminNovelHandler.auditLogger.Log(actorID, actorName, "novel.deleted", "novel", novelID,
		map[string]any{"title": novel.Title})
	responseWriter.WriteHeader(http.StatusNoContent)
}

// Restore: POST /admin/novels/{id}/restore — reverses Delete. Fetches
// the novel *after* restoring (Get's own deleted_at IS NULL filter
// would otherwise 404 on a still-hidden novel), same reasoning Delete
// uses in reverse.
func (adminNovelHandler *AdminNovelHandler) Restore(responseWriter http.ResponseWriter, request *http.Request) {
	novelID := request.PathValue("id")
	if err := adminNovelHandler.novelService.Restore(request.Context(), novelID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	novel, err := adminNovelHandler.novelService.Get(request.Context(), novelID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	adminNovelHandler.auditLogger.Log(actorID, actorName, "novel.restored", "novel", novelID,
		map[string]any{"title": novel.Title})
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}

type novelPositionRequest struct {
	ID        string `json:"id"`
	SortOrder int    `json:"sort_order"`
}

type novelReorderRequest struct {
	Items []novelPositionRequest `json:"items"`
}

// Reorder: PUT /admin/novels/reorder — sets each given novel's
// sort_order exactly as provided. The dashboard sends absolute
// positions (page offset + local index) so dragging within one page
// never collides with untouched novels on other pages.
func (adminNovelHandler *AdminNovelHandler) Reorder(responseWriter http.ResponseWriter, request *http.Request) {
	var reorderRequest novelReorderRequest
	if !decodeJSON(responseWriter, request, &reorderRequest) {
		return
	}
	positions := make([]repository.NovelPosition, 0, len(reorderRequest.Items))
	for _, item := range reorderRequest.Items {
		positions = append(positions, repository.NovelPosition{NovelID: item.ID, SortOrder: item.SortOrder})
	}
	if err := adminNovelHandler.novelService.Reorder(request.Context(), positions); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	adminNovelHandler.auditLogger.Log(actorID, actorName, "novel.reordered", "novel", "",
		map[string]any{"count": len(positions)})
	responseWriter.WriteHeader(http.StatusNoContent)
}

// UpdateCover stores an uploaded image (multipart field "cover").
func (adminNovelHandler *AdminNovelHandler) UpdateCover(responseWriter http.ResponseWriter, request *http.Request) {
	novelID := request.PathValue("id")

	coverURL, err := saveUploadedImage(
		request, "cover", adminNovelHandler.coversDirectory,
		adminNovelHandler.coversUrlPrefix, novelID)
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}

	if err := adminNovelHandler.novelService.UpdateCover(request.Context(), novelID, coverURL); err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	novel, err := adminNovelHandler.novelService.Get(request.Context(), novelID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}

type novelRatingResponse struct {
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Rating    int       `json:"rating"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Ratings: GET /admin/novels/{id}/ratings — the moderation view behind
// AdminNovelHandler.DeleteRating: every individual reader rating on
// this novel, so an admin can find and remove a specific abusive/spam
// one. There's deliberately no edit here — see NovelService.ListRatings.
func (adminNovelHandler *AdminNovelHandler) Ratings(responseWriter http.ResponseWriter, request *http.Request) {
	ratings, err := adminNovelHandler.novelService.ListRatings(request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]novelRatingResponse, 0, len(ratings))
	for _, rating := range ratings {
		items = append(items, novelRatingResponse{
			UserID:    rating.UserID,
			Username:  rating.Username,
			Rating:    rating.Rating,
			CreatedAt: rating.CreatedAt,
			UpdatedAt: rating.UpdatedAt,
		})
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

// DeleteRating: DELETE /admin/novels/{id}/ratings/{userId} — moderation
// removal of one reader's rating (abuse/spam), audit-logged the same as
// any other admin action; never a hand-edit of the rating's value.
func (adminNovelHandler *AdminNovelHandler) DeleteRating(responseWriter http.ResponseWriter, request *http.Request) {
	novelID := request.PathValue("id")
	userID := request.PathValue("userId")

	if _, err := adminNovelHandler.novelService.AdminRemoveRating(request.Context(), novelID, userID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	adminNovelHandler.auditLogger.Log(actorID, actorName, "novel_rating.deleted", "novel", novelID,
		map[string]any{"rated_by_user_id": userID})
	responseWriter.WriteHeader(http.StatusNoContent)
}
