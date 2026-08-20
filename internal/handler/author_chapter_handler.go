package handler

import (
	"net/http"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

// AuthorChapterHandler lets an author manage chapters of their own
// novels — the same ChapterService the admin dashboard uses, scoped
// by ownership. Reuses chapterWriteRequest/chapterResponse/etc. from
// admin_chapter_handler.go (same package, same shape).
type AuthorChapterHandler struct {
	chapterService *service.ChapterService
	novelService   *service.NovelService
	reports        *repository.NovelReportRepository
	auditLogger    *audit.Logger
}

func NewAuthorChapterHandler(chapterService *service.ChapterService, novelService *service.NovelService, reports *repository.NovelReportRepository, auditLogger *audit.Logger) *AuthorChapterHandler {
	return &AuthorChapterHandler{chapterService: chapterService, novelService: novelService, reports: reports, auditLogger: auditLogger}
}

// blockIfHeld stops an author from republishing (immediately or on a
// schedule) around an admin hold — the only door back to "published"
// is the release-request flow, which needs an admin's actual approval,
// not just a submission. Never called from the admin-restore or
// scheduled-auto-publish paths, which go through ChapterService
// directly and never touch this handler.
func (authorChapterHandler *AuthorChapterHandler) blockIfHeld(request *http.Request, chapterID string) error {
	held, err := authorChapterHandler.reports.HasActiveChapterHold(request.Context(), chapterID)
	if err != nil {
		return err
	}
	if held {
		return &service.ValidationError{Message: "this chapter is on hold by admin — request a release review from Reports before publishing"}
	}
	return nil
}

// loadOwnedChapter fetches a chapter and verifies callerUserID owns
// the novel it belongs to — 404s (ErrChapterNotFound) whether the
// chapter doesn't exist or its novel isn't the caller's.
func loadOwnedChapter(request *http.Request, authorChapterHandler *AuthorChapterHandler, chapterID, callerUserID string) (*repository.Chapter, error) {
	chapter, err := authorChapterHandler.chapterService.Get(request.Context(), chapterID)
	if err != nil {
		return nil, err
	}
	if _, err := loadOwnedNovel(request, authorChapterHandler.novelService, chapter.NovelID, callerUserID); err != nil {
		return nil, repository.ErrChapterNotFound
	}
	return chapter, nil
}

// ListByNovel: GET /author/novels/{id}/chapters
func (authorChapterHandler *AuthorChapterHandler) ListByNovel(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novelID := request.PathValue("id")
	if _, err := loadOwnedNovel(request, authorChapterHandler.novelService, novelID, callerUserID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	chapters, err := authorChapterHandler.chapterService.ListByNovel(request.Context(), novelID)
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

// Create: POST /author/novels/{id}/chapters — one draft chapter.
func (authorChapterHandler *AuthorChapterHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novelID := request.PathValue("id")
	if _, err := loadOwnedNovel(request, authorChapterHandler.novelService, novelID, callerUserID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	var writeRequest chapterWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	chapter, err := authorChapterHandler.chapterService.CreateOne(request.Context(), novelID, writeRequest.toWrite())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusCreated, newChapterResponse(chapter))
}

// Get: GET /author/chapters/{id} — full content.
func (authorChapterHandler *AuthorChapterHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	chapter, err := loadOwnedChapter(request, authorChapterHandler, request.PathValue("id"), callerUserID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}

// Update: PUT /author/chapters/{id}
func (authorChapterHandler *AuthorChapterHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	chapterID := request.PathValue("id")
	if _, err := loadOwnedChapter(request, authorChapterHandler, chapterID, callerUserID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	var writeRequest chapterWriteRequest
	if !decodeLargeJSON(responseWriter, request, &writeRequest) {
		return
	}
	chapter, err := authorChapterHandler.chapterService.Update(request.Context(), chapterID, writeRequest.toWrite())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}

// UpdateStatus: PUT /author/chapters/{id}/status — draft ↔ published.
func (authorChapterHandler *AuthorChapterHandler) UpdateStatus(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	chapterID := request.PathValue("id")
	if _, err := loadOwnedChapter(request, authorChapterHandler, chapterID, callerUserID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	var statusRequest chapterStatusRequest
	if !decodeJSON(responseWriter, request, &statusRequest) {
		return
	}
	if statusRequest.Status == "published" {
		if err := authorChapterHandler.blockIfHeld(request, chapterID); err != nil {
			writeServiceError(responseWriter, err)
			return
		}
	}
	chapter, err := authorChapterHandler.chapterService.UpdateStatus(request.Context(), chapterID, statusRequest.Status)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	authorChapterHandler.auditLogger.Log(actorID, actorName, "chapter."+chapter.Status, "chapter", chapter.ID,
		map[string]any{"novel_id": chapter.NovelID, "number": chapter.Number, "title": chapter.Title})
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}

// Schedule: PUT /author/chapters/{id}/schedule — set or clear a
// draft's auto-publish time.
func (authorChapterHandler *AuthorChapterHandler) Schedule(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	chapterID := request.PathValue("id")
	if _, err := loadOwnedChapter(request, authorChapterHandler, chapterID, callerUserID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	var scheduleRequest chapterScheduleRequest
	if !decodeJSON(responseWriter, request, &scheduleRequest) {
		return
	}

	var chapter *repository.Chapter
	var err error
	var action string
	if scheduleRequest.ScheduledAt != nil {
		if err := authorChapterHandler.blockIfHeld(request, chapterID); err != nil {
			writeServiceError(responseWriter, err)
			return
		}
		chapter, err = authorChapterHandler.chapterService.Schedule(request.Context(), chapterID, *scheduleRequest.ScheduledAt)
		action = "chapter.scheduled"
	} else {
		chapter, err = authorChapterHandler.chapterService.Unschedule(request.Context(), chapterID)
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
	authorChapterHandler.auditLogger.Log(actorID, actorName, action, "chapter", chapter.ID, details)
	writeJSON(responseWriter, http.StatusOK, newChapterResponse(chapter))
}

// Import: POST /author/novels/{id}/chapters/import — bulk-add chapters
// extracted client-side from a PDF/text manuscript. Reuses
// importChaptersRequest/chapterWriteRequest from admin_chapter_handler.go
// (same package) and decodeLargeJSON since extracted text can be large.
// A chapter whose title matches one this novel already has replaces
// that chapter's content in place; everything else is appended as a
// new draft — see ChapterService.Import.
func (authorChapterHandler *AuthorChapterHandler) Import(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novel, err := loadOwnedNovel(request, authorChapterHandler.novelService, request.PathValue("id"), callerUserID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	var importRequest importChaptersRequest
	if !decodeLargeJSON(responseWriter, request, &importRequest) {
		return
	}
	writes := make([]repository.ChapterWrite, 0, len(importRequest.Chapters))
	for _, chapterRequest := range importRequest.Chapters {
		writes = append(writes, chapterRequest.toWrite())
	}

	created, updated, err := authorChapterHandler.chapterService.Import(request.Context(), novel.ID, writes)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	authorChapterHandler.auditLogger.Log(actorID, actorName, "chapter.imported", "novel", novel.ID,
		map[string]any{"created": created, "updated": updated})
	writeJSON(responseWriter, http.StatusCreated, map[string]any{"created": created, "updated": updated})
}

// Delete: DELETE /author/chapters/{id}
func (authorChapterHandler *AuthorChapterHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	chapterID := request.PathValue("id")
	chapter, err := loadOwnedChapter(request, authorChapterHandler, chapterID, callerUserID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	if err := authorChapterHandler.chapterService.Delete(request.Context(), chapterID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	authorChapterHandler.auditLogger.Log(actorID, actorName, "chapter.deleted", "chapter", chapterID,
		map[string]any{"novel_id": chapter.NovelID, "number": chapter.Number, "title": chapter.Title})
	responseWriter.WriteHeader(http.StatusNoContent)
}
