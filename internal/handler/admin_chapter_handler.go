package handler

import (
	"net/http"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

type AdminChapterHandler struct {
	chapterService *service.ChapterService
	auditLogger    *audit.Logger
}

func NewAdminChapterHandler(chapterService *service.ChapterService, auditLogger *audit.Logger) *AdminChapterHandler {
	return &AdminChapterHandler{chapterService: chapterService, auditLogger: auditLogger}
}

type chapterWriteRequest struct {
	Title       string `json:"title"`
	ContentJSON string `json:"content_json"`
	ContentText string `json:"content_text"`
}

func (writeRequest chapterWriteRequest) toWrite() repository.ChapterWrite {
	return repository.ChapterWrite{
		Title:       writeRequest.Title,
		ContentJSON: writeRequest.ContentJSON,
		ContentText: writeRequest.ContentText,
	}
}

// chapterListItemResponse is the lean list shape (no content).
type chapterListItemResponse struct {
	ID          string     `json:"id"`
	NovelID     string     `json:"novel_id"`
	Number      int        `json:"number"`
	Title       string     `json:"title"`
	WordCount   int        `json:"word_count"`
	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at"`
	ScheduledAt *time.Time `json:"scheduled_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type chapterResponse struct {
	chapterListItemResponse
	ContentJSON string `json:"content_json"`
	ContentText string `json:"content_text"`
}

func newChapterListItemResponse(chapter *repository.Chapter) chapterListItemResponse {
	return chapterListItemResponse{
		ID:          chapter.ID,
		NovelID:     chapter.NovelID,
		Number:      chapter.Number,
		Title:       chapter.Title,
		WordCount:   chapter.WordCount,
		Status:      chapter.Status,
		PublishedAt: chapter.PublishedAt,
		ScheduledAt: chapter.ScheduledAt,
		UpdatedAt:   chapter.UpdatedAt,
	}
}

func newChapterResponse(chapter *repository.Chapter) chapterResponse {
	return chapterResponse{
		chapterListItemResponse: newChapterListItemResponse(chapter),
		ContentJSON:             chapter.ContentJSON,
		ContentText:             chapter.ContentText,
	}
}

// ListByNovel: GET /admin/novels/{id}/chapters
func (adminChapterHandler *AdminChapterHandler) ListByNovel(responseWriter http.ResponseWriter, request *http.Request) {
	chapters, err := adminChapterHandler.chapterService.ListByNovel(request.Context(), request.PathValue("id"))
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

// Create: POST /admin/novels/{id}/chapters — one draft chapter.
func (adminChapterHandler *AdminChapterHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest chapterWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	chapter, err := adminChapterHandler.chapterService.CreateOne(
		request.Context(), request.PathValue("id"), writeRequest.toWrite())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusCreated, newChapterResponse(chapter))
}

type importChaptersRequest struct {
	Chapters []chapterWriteRequest `json:"chapters"`
}

// Import: POST /admin/novels/{id}/chapters/import — batch of drafts.
func (adminChapterHandler *AdminChapterHandler) Import(responseWriter http.ResponseWriter, request *http.Request) {
	var importRequest importChaptersRequest
	if !decodeLargeJSON(responseWriter, request, &importRequest) {
		return
	}
	writes := make([]repository.ChapterWrite, 0, len(importRequest.Chapters))
	for _, chapterRequest := range importRequest.Chapters {
		writes = append(writes, chapterRequest.toWrite())
	}

	created, err := adminChapterHandler.chapterService.Import(
		request.Context(), request.PathValue("id"), writes)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusCreated, map[string]any{"created": created})
}

// Get: GET /admin/chapters/{id} — full content.
func (adminChapterHandler *AdminChapterHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	chapter, err := adminChapterHandler.chapterService.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}

// Update: PUT /admin/chapters/{id}
func (adminChapterHandler *AdminChapterHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest chapterWriteRequest
	if !decodeLargeJSON(responseWriter, request, &writeRequest) {
		return
	}
	chapter, err := adminChapterHandler.chapterService.Update(
		request.Context(), request.PathValue("id"), writeRequest.toWrite())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}

type chapterStatusRequest struct {
	Status string `json:"status"`
}

// UpdateStatus: PUT /admin/chapters/{id}/status — draft ↔ published.
func (adminChapterHandler *AdminChapterHandler) UpdateStatus(responseWriter http.ResponseWriter, request *http.Request) {
	var statusRequest chapterStatusRequest
	if !decodeJSON(responseWriter, request, &statusRequest) {
		return
	}
	chapter, err := adminChapterHandler.chapterService.UpdateStatus(
		request.Context(), request.PathValue("id"), statusRequest.Status)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	adminChapterHandler.auditLogger.Log(actorID, actorName, "chapter."+chapter.Status, "chapter", chapter.ID,
		map[string]any{"novel_id": chapter.NovelID, "number": chapter.Number, "title": chapter.Title})
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}

type chapterScheduleRequest struct {
	// RFC3339; null/omitted clears the schedule.
	ScheduledAt *time.Time `json:"scheduled_at"`
}

// Schedule: PUT /admin/chapters/{id}/schedule — set or clear a
// draft's auto-publish time.
func (adminChapterHandler *AdminChapterHandler) Schedule(responseWriter http.ResponseWriter, request *http.Request) {
	var scheduleRequest chapterScheduleRequest
	if !decodeJSON(responseWriter, request, &scheduleRequest) {
		return
	}

	chapterID := request.PathValue("id")
	var chapter *repository.Chapter
	var err error
	var action string
	if scheduleRequest.ScheduledAt != nil {
		chapter, err = adminChapterHandler.chapterService.Schedule(request.Context(), chapterID, *scheduleRequest.ScheduledAt)
		action = "chapter.scheduled"
	} else {
		chapter, err = adminChapterHandler.chapterService.Unschedule(request.Context(), chapterID)
		action = "chapter.unscheduled"
	}
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	actorID, actorName := actorFromContext(request.Context())
	details := map[string]any{"novel_id": chapter.NovelID, "number": chapter.Number, "title": chapter.Title}
	if chapter.ScheduledAt != nil {
		details["scheduled_at"] = chapter.ScheduledAt
	}
	adminChapterHandler.auditLogger.Log(actorID, actorName, action, "chapter", chapter.ID, details)
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}

// Delete: DELETE /admin/chapters/{id}
func (adminChapterHandler *AdminChapterHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	chapterID := request.PathValue("id")
	chapter, err := adminChapterHandler.chapterService.Get(request.Context(), chapterID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	if err := adminChapterHandler.chapterService.Delete(request.Context(), chapterID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	adminChapterHandler.auditLogger.Log(actorID, actorName, "chapter.deleted", "chapter", chapterID,
		map[string]any{"novel_id": chapter.NovelID, "number": chapter.Number, "title": chapter.Title})
	responseWriter.WriteHeader(http.StatusNoContent)
}
