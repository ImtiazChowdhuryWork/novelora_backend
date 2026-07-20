package handler

import (
	"net/http"
	"strconv"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

// PublicNovelHandler serves the reader-facing catalog: no auth, no
// drafts, lean payloads (the app's Discover/Library/reader feed here).
type PublicNovelHandler struct {
	novelService   *service.NovelService
	chapterService *service.ChapterService
}

func NewPublicNovelHandler(novelService *service.NovelService, chapterService *service.ChapterService) *PublicNovelHandler {
	return &PublicNovelHandler{novelService: novelService, chapterService: chapterService}
}

func parseOptionalBool(value string) *bool {
	switch value {
	case "true":
		result := true
		return &result
	case "false":
		result := false
		return &result
	default:
		return nil
	}
}

// List: GET /novels?page=&page_size=&search=&status=&is_short=&recommended=
func (publicNovelHandler *PublicNovelHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	filter := repository.NovelListFilter{
		Search:        query.Get("search"),
		Status:        query.Get("status"),
		IsShort:       parseOptionalBool(query.Get("is_short")),
		IsRecommended: parseOptionalBool(query.Get("recommended")),
		GenreID:       query.Get("genre_id"),
		Sort:          query.Get("sort"),
		Page:          page,
		PageSize:      pageSize,
	}

	novels, total, err := publicNovelHandler.novelService.List(request.Context(), filter)
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

// Get: GET /novels/{id}
func (publicNovelHandler *PublicNovelHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	novel, err := publicNovelHandler.novelService.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}

// Chapters: GET /novels/{id}/chapters — published only, no content.
func (publicNovelHandler *PublicNovelHandler) Chapters(responseWriter http.ResponseWriter, request *http.Request) {
	chapters, err := publicNovelHandler.chapterService.ListPublishedByNovel(
		request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]chapterListItemResponse, 0, len(chapters))
	for _, chapter := range chapters {
		items = append(items, newChapterListItemResponse(chapter))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

// Chapter: GET /chapters/{id} — full content; drafts 404.
func (publicNovelHandler *PublicNovelHandler) Chapter(responseWriter http.ResponseWriter, request *http.Request) {
	chapter, err := publicNovelHandler.chapterService.GetPublished(
		request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}
