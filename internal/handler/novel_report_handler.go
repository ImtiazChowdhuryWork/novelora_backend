package handler

import (
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

// NovelReportHandler serves Book Detail's flag-icon report feature.
// Create/Delete require login and are scoped to the caller's own
// report (same as ratings/comments); List/Get/UpdateStatus are
// admin-only moderation.
type NovelReportHandler struct {
	reports            *service.NovelReportService
	auditLogger        *audit.Logger
	reportImagesDir    string
	reportImagesPrefix string
}

func NewNovelReportHandler(reports *service.NovelReportService, auditLogger *audit.Logger, uploadsDirectory string) *NovelReportHandler {
	return &NovelReportHandler{
		reports:            reports,
		auditLogger:        auditLogger,
		reportImagesDir:    filepath.Join(uploadsDirectory, "reports"),
		reportImagesPrefix: "/uploads/reports/",
	}
}

type reportResponse struct {
	ID             string     `json:"id"`
	NovelID        string     `json:"novel_id"`
	NovelTitle     string     `json:"novel_title"`
	AuthorName     string     `json:"author_name"`
	UserID         string     `json:"user_id"`
	Username       string     `json:"username"`
	Reason         string     `json:"reason"`
	Details        string     `json:"details"`
	ChapterID      *string    `json:"chapter_id"`
	ChapterTitle   *string    `json:"chapter_title"`
	ImageCount     int        `json:"image_count"`
	Images         []string   `json:"images"`
	Status         string     `json:"status"`
	ResolutionNote string     `json:"resolution_note"`
	ReviewedByName string     `json:"reviewed_by_name"`
	ReviewedAt     *time.Time `json:"reviewed_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

func newReportResponse(report *repository.NovelReport) reportResponse {
	imageURLs := make([]string, 0, len(report.Images))
	for _, image := range report.Images {
		imageURLs = append(imageURLs, image.ImageURL)
	}
	return reportResponse{
		ID:             report.ID,
		NovelID:        report.NovelID,
		NovelTitle:     report.NovelTitle,
		AuthorName:     report.AuthorName,
		UserID:         report.UserID,
		Username:       report.Username,
		Reason:         report.Reason,
		Details:        report.Details,
		ChapterID:      report.ChapterID,
		ChapterTitle:   report.ChapterTitle,
		ImageCount:     report.ImageCount,
		Images:         imageURLs,
		Status:         report.Status,
		ResolutionNote: report.ResolutionNote,
		ReviewedByName: report.ReviewedByName,
		ReviewedAt:     report.ReviewedAt,
		CreatedAt:      report.CreatedAt,
	}
}

// Create: POST /novels/{id}/report — multipart form (reason, details,
// optional chapter_id, up to 3 files under "images"). Requires the
// Authenticate middleware. Images are saved to disk under a
// fresh-generated id (not the report's own id, which doesn't exist
// yet at this point) before the report row is created.
func (handler *NovelReportHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	imageURLs, err := saveUploadedReportImages(
		request, "images", handler.reportImagesDir, handler.reportImagesPrefix, uuid.NewString())
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}

	reason := request.FormValue("reason")
	details := request.FormValue("details")
	var chapterID *string
	if value := request.FormValue("chapter_id"); value != "" {
		chapterID = &value
	}

	report, err := handler.reports.Create(
		request.Context(), request.PathValue("id"), userID, reason, details, chapterID, imageURLs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusCreated, newReportResponse(report))
}

// Delete: DELETE /reports/{id} — own report only. Requires the
// Authenticate middleware.
func (handler *NovelReportHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	if err := handler.reports.Delete(request.Context(), request.PathValue("id"), userID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}

// List: GET /admin/reports?status=&page=&page_size=
func (handler *NovelReportHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	reports, total, err := handler.reports.List(request.Context(), query.Get("status"), page, pageSize)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]reportResponse, 0, len(reports))
	for _, report := range reports {
		items = append(items, newReportResponse(report))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items, "total": total})
}

// Get: GET /admin/reports/{id} — the dashboard's report detail view
// (full record + evidence images).
func (handler *NovelReportHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	report, err := handler.reports.GetDetail(request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}

type updateReportStatusRequest struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

// UpdateStatus: PUT /admin/reports/{id}/status — audit-logged the same
// as admin's other single-item moderation actions. Moving off
// "pending" notifies the reporting reader (see NovelReportService).
func (handler *NovelReportHandler) UpdateStatus(responseWriter http.ResponseWriter, request *http.Request) {
	reportID := request.PathValue("id")

	var body updateReportStatusRequest
	if !decodeJSON(responseWriter, request, &body) {
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	report, err := handler.reports.UpdateStatus(request.Context(), reportID, body.Status, body.Note, actorID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	handler.auditLogger.Log(actorID, actorName, "report."+body.Status, "report", reportID, nil)
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}

// Mine: GET /users/me/reports?page=&page_size= — the caller's own
// report history, backing the app's "My Reports" Profile page.
// Requires the Authenticate middleware.
func (handler *NovelReportHandler) Mine(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	reports, total, err := handler.reports.ListForUser(request.Context(), userID, page, pageSize)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]reportResponse, 0, len(reports))
	for _, report := range reports {
		items = append(items, newReportResponse(report))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items, "total": total})
}
