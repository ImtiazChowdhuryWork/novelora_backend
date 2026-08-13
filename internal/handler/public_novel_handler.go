package handler

import (
	"net/http"
	"strconv"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

// PublicNovelHandler serves the reader-facing catalog: no auth required,
// no drafts, lean payloads (the app's Discover/Library/reader feed here).
// A Bearer token is still read when present (see optionalUserID) so a
// logged-in reader's chapter opens can be attributed to their account
// for reading history — guests remain fully served either way.
type PublicNovelHandler struct {
	novelService   *service.NovelService
	chapterService *service.ChapterService
	readingHistory *service.ReadingHistoryService
	reports        *service.NovelReportService
	jwtSecret      []byte
}

func NewPublicNovelHandler(
	novelService *service.NovelService,
	chapterService *service.ChapterService,
	readingHistory *service.ReadingHistoryService,
	reports *service.NovelReportService,
	jwtSecret []byte,
) *PublicNovelHandler {
	return &PublicNovelHandler{
		novelService:   novelService,
		chapterService: chapterService,
		readingHistory: readingHistory,
		reports:        reports,
		jwtSecret:      jwtSecret,
	}
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

// List: GET /novels?page=&page_size=&search=&status=&is_short=&recommended=&exclusive=&personalize=
func (publicNovelHandler *PublicNovelHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	filter := repository.NovelListFilter{
		Search:        query.Get("search"),
		Status:        query.Get("status"),
		IsShort:       parseOptionalBool(query.Get("is_short")),
		IsRecommended: parseOptionalBool(query.Get("recommended")),
		IsExclusive:   parseOptionalBool(query.Get("exclusive")),
		GenreID:       query.Get("genre_id"),
		Sort:          query.Get("sort"),
		Page:          page,
		PageSize:      pageSize,
	}

	// Phase 5e: opt-in per-caller personalization (e.g. the app's Picks
	// For You feed) — a guest, or a request that didn't ask for it,
	// passes through unchanged. Never overrides an explicit genre_id.
	if query.Get("personalize") == "true" {
		if userID, ok := middleware.OptionalUserID(publicNovelHandler.jwtSecret, request); ok {
			filter = publicNovelHandler.novelService.PersonalizeFilter(request.Context(), filter, userID)
		}
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

// novelDetailResponse adds the logged-in caller's own rating and
// support state on top of the shared novelResponse shape — per-viewer,
// so only Get (and the rating/support endpoints below) return it,
// never List or the admin handler.
type novelDetailResponse struct {
	novelResponse
	MyRating       *int `json:"my_rating"`
	MySupport      bool `json:"my_support"`
	IsReportedByMe bool `json:"is_reported_by_me"`
}

func newNovelDetailResponse(novel *repository.Novel, myRating *int, mySupport, isReportedByMe bool) novelDetailResponse {
	return novelDetailResponse{
		novelResponse:  newNovelResponse(novel),
		MyRating:       myRating,
		MySupport:      mySupport,
		IsReportedByMe: isReportedByMe,
	}
}

// Get: GET /novels/{id}
func (publicNovelHandler *PublicNovelHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	novelID := request.PathValue("id")
	novel, err := publicNovelHandler.novelService.Get(request.Context(), novelID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	// Real read, counts as a view — unlike AdminNovelHandler.Get, which
	// shares NovelService.Get but must never record a view for an admin
	// opening the edit form.
	publicNovelHandler.novelService.RecordView(request.Context(), novelID)
	novel.ViewCount++

	var myRating *int
	var mySupport, isReportedByMe bool
	if userID, ok := middleware.OptionalUserID(publicNovelHandler.jwtSecret, request); ok {
		myRating, err = publicNovelHandler.novelService.GetUserRating(request.Context(), novelID, userID)
		if err != nil {
			writeServiceError(responseWriter, err)
			return
		}
		mySupport, err = publicNovelHandler.novelService.IsSupportedByUser(request.Context(), novelID, userID)
		if err != nil {
			writeServiceError(responseWriter, err)
			return
		}
		isReportedByMe, err = publicNovelHandler.reports.HasReported(request.Context(), novelID, userID)
		if err != nil {
			writeServiceError(responseWriter, err)
			return
		}
	}
	writeJSON(responseWriter, http.StatusOK, newNovelDetailResponse(novel, myRating, mySupport, isReportedByMe))
}

type rateNovelRequest struct {
	Rating int `json:"rating"`
}

// Rate: PUT /novels/{id}/rating — submits or updates the caller's own
// 1-5 star rating. Requires the Authenticate middleware.
func (publicNovelHandler *PublicNovelHandler) Rate(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novelID := request.PathValue("id")

	var body rateNovelRequest
	if !decodeJSON(responseWriter, request, &body) {
		return
	}
	novel, err := publicNovelHandler.novelService.Rate(request.Context(), novelID, userID, body.Rating)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	mySupport, err := publicNovelHandler.novelService.IsSupportedByUser(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	isReportedByMe, err := publicNovelHandler.reports.HasReported(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	myRating := body.Rating
	writeJSON(responseWriter, http.StatusOK, newNovelDetailResponse(novel, &myRating, mySupport, isReportedByMe))
}

// RemoveRating: DELETE /novels/{id}/rating — withdraws the caller's own
// rating. Requires the Authenticate middleware.
func (publicNovelHandler *PublicNovelHandler) RemoveRating(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novelID := request.PathValue("id")

	novel, err := publicNovelHandler.novelService.RemoveRating(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	mySupport, err := publicNovelHandler.novelService.IsSupportedByUser(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	isReportedByMe, err := publicNovelHandler.reports.HasReported(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelDetailResponse(novel, nil, mySupport, isReportedByMe))
}

// Support: PUT /novels/{id}/support — a free "Support" tap (Phase 5d,
// no money involved). Requires the Authenticate middleware.
func (publicNovelHandler *PublicNovelHandler) Support(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novelID := request.PathValue("id")

	novel, err := publicNovelHandler.novelService.Support(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	myRating, err := publicNovelHandler.novelService.GetUserRating(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	isReportedByMe, err := publicNovelHandler.reports.HasReported(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelDetailResponse(novel, myRating, true, isReportedByMe))
}

// Unsupport: DELETE /novels/{id}/support — withdraws the caller's own support.
func (publicNovelHandler *PublicNovelHandler) Unsupport(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novelID := request.PathValue("id")

	novel, err := publicNovelHandler.novelService.Unsupport(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	myRating, err := publicNovelHandler.novelService.GetUserRating(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	isReportedByMe, err := publicNovelHandler.reports.HasReported(request.Context(), novelID, userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelDetailResponse(novel, myRating, false, isReportedByMe))
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
	// Best-effort, logged-in readers only — guests aren't tracked, see
	// Phase 5a's plan note.
	if userID, ok := middleware.OptionalUserID(publicNovelHandler.jwtSecret, request); ok {
		publicNovelHandler.readingHistory.RecordRead(request.Context(), userID, chapter.NovelID, chapter.ID)
	}
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}
