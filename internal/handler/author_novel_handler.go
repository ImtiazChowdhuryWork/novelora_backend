package handler

import (
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

// AuthorNovelHandler lets an author manage their own novels — the
// same NovelService the admin dashboard uses, scoped by ownership
// (see loadOwnedNovel) instead of admin's unrestricted access.
// Editorial flags (is_recommended, is_exclusive, the admin-typed
// baseline rating, the manual view-count override) stay admin-only:
// Create always zeroes them, Update always preserves whatever they
// already were regardless of what the request body sends.
type AuthorNovelHandler struct {
	novelService    *service.NovelService
	auditLogger     *audit.Logger
	coversDirectory string
	coversUrlPrefix string
}

func NewAuthorNovelHandler(novelService *service.NovelService, auditLogger *audit.Logger, uploadsDirectory string) *AuthorNovelHandler {
	return &AuthorNovelHandler{
		novelService:    novelService,
		auditLogger:     auditLogger,
		coversDirectory: filepath.Join(uploadsDirectory, "covers"),
		coversUrlPrefix: "/uploads/covers/",
	}
}

// loadOwnedNovel fetches a novel and verifies callerUserID owns it —
// 404s (via ErrNovelNotFound, not a separate 403) whether the novel
// doesn't exist or simply isn't the caller's, the same non-leaking
// shape as NovelReportRepository.Delete's ownership scoping.
func loadOwnedNovel(request *http.Request, novelService *service.NovelService, novelID, callerUserID string) (*repository.Novel, error) {
	novel, err := novelService.Get(request.Context(), novelID)
	if err != nil {
		return nil, err
	}
	if novel.OwnerUserID == nil || *novel.OwnerUserID != callerUserID {
		return nil, repository.ErrNovelNotFound
	}
	return novel, nil
}

// List: GET /author/novels?page=&page_size=&search=&status=&sort= —
// scoped to the caller's own novels only.
func (authorNovelHandler *AuthorNovelHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	filter := repository.NovelListFilter{
		Search: query.Get("search"),
		Status: query.Get("status"),
		Sort:   query.Get("sort"),
		OwnerUserID: &callerUserID,
		// An author needs to see a held novel, not have it silently
		// vanish — IncludeHidden is safe here specifically because it's
		// always paired with OwnerUserID, so this can only ever surface
		// the caller's own hidden novels, never anyone else's.
		IncludeHidden: true,
		Page:          page,
		PageSize:      pageSize,
	}

	novels, total, err := authorNovelHandler.novelService.List(request.Context(), filter)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]novelResponse, 0, len(novels))
	for _, novel := range novels {
		items = append(items, newNovelResponse(novel))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items, "total": total})
}

func (authorNovelHandler *AuthorNovelHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novel, err := loadOwnedNovel(request, authorNovelHandler.novelService, request.PathValue("id"), callerUserID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}

// Create: POST /author/novels — always owned by the caller, and
// always starts with every editorial flag off, regardless of what (if
// anything) the request sends for them.
func (authorNovelHandler *AuthorNovelHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	var writeRequest novelWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	write := writeRequest.toWrite()
	write.OwnerUserID = &callerUserID
	write.Rating = nil
	write.ViewCount = 0
	write.IsRecommended = false
	write.IsExclusive = false

	novel, err := authorNovelHandler.novelService.Create(request.Context(), write, writeRequest.GenreIDs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	authorNovelHandler.auditLogger.Log(actorID, actorName, "novel.created", "novel", novel.ID,
		map[string]any{"title": novel.Title})
	writeJSON(responseWriter, http.StatusCreated, newNovelResponse(novel))
}

func (authorNovelHandler *AuthorNovelHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novelID := request.PathValue("id")
	existing, err := loadOwnedNovel(request, authorNovelHandler.novelService, novelID, callerUserID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	var writeRequest novelWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	write := writeRequest.toWrite()
	// Preserve whatever these already were — never take them from the
	// request body, since the author dashboard's form doesn't (and
	// shouldn't) expose them.
	write.Rating = existing.Rating
	write.ViewCount = existing.ViewCount
	write.IsRecommended = existing.IsRecommended
	write.IsExclusive = existing.IsExclusive

	novel, err := authorNovelHandler.novelService.Update(request.Context(), novelID, write, writeRequest.GenreIDs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	authorNovelHandler.auditLogger.Log(actorID, actorName, "novel.updated", "novel", novel.ID,
		map[string]any{"title": novel.Title})
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}

// Delete: DELETE /author/novels/{id} — an author withdrawing their own
// novel. Soft-delete, same as admin's, so recovery stays possible.
func (authorNovelHandler *AuthorNovelHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novel, err := loadOwnedNovel(request, authorNovelHandler.novelService, request.PathValue("id"), callerUserID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	if err := authorNovelHandler.novelService.Delete(request.Context(), novel.ID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	authorNovelHandler.auditLogger.Log(actorID, actorName, "novel.deleted", "novel", novel.ID,
		map[string]any{"title": novel.Title})
	responseWriter.WriteHeader(http.StatusNoContent)
}

// UpdateCover: PUT /author/novels/{id}/cover — multipart field "cover".
func (authorNovelHandler *AuthorNovelHandler) UpdateCover(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novelID := request.PathValue("id")
	if _, err := loadOwnedNovel(request, authorNovelHandler.novelService, novelID, callerUserID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	coverURL, err := saveUploadedImage(
		request, "cover", authorNovelHandler.coversDirectory,
		authorNovelHandler.coversUrlPrefix, novelID)
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}
	if err := authorNovelHandler.novelService.UpdateCover(request.Context(), novelID, coverURL); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	novel, err := authorNovelHandler.novelService.Get(request.Context(), novelID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}
