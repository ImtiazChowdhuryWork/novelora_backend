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

// NovelReportHandler serves Book Detail's flag-icon report feature and
// — as of the moderation workflow v2 redesign — the full Reporter/
// Admin/Author state machine: Create/Delete require login and are
// scoped to the caller's own report (same as ratings/comments);
// List/Get and every decision (Reject/HoldChapter/HoldNovel/
// ResolveDirect/ApproveRelease/RejectRelease) are admin-only;
// SubmitReleaseRequest/ListForOwner are author-only, scoped to novels
// they own.
type NovelReportHandler struct {
	reports             *service.NovelReportService
	auditLogger         *audit.Logger
	reportImagesDir     string
	reportImagesPrefix  string
	releaseImagesDir    string
	releaseImagesPrefix string
	holdImagesDir       string
	holdImagesPrefix    string
}

func NewNovelReportHandler(reports *service.NovelReportService, auditLogger *audit.Logger, uploadsDirectory string) *NovelReportHandler {
	return &NovelReportHandler{
		reports:             reports,
		auditLogger:         auditLogger,
		reportImagesDir:     filepath.Join(uploadsDirectory, "reports"),
		reportImagesPrefix:  "/uploads/reports/",
		releaseImagesDir:    filepath.Join(uploadsDirectory, "release-requests"),
		releaseImagesPrefix: "/uploads/release-requests/",
		holdImagesDir:       filepath.Join(uploadsDirectory, "moderation-evidence"),
		holdImagesPrefix:    "/uploads/moderation-evidence/",
	}
}

type reportResponse struct {
	ID          string  `json:"id"`
	NovelID     string  `json:"novel_id"`
	NovelTitle  string  `json:"novel_title"`
	AuthorName  string  `json:"author_name"`
	OwnerUserID *string `json:"owner_user_id"`
	UserID      string  `json:"user_id"`
	Username    string  `json:"username"`
	Reason      string  `json:"reason"`
	Details     string  `json:"details"`
	ChapterID   *string `json:"chapter_id"`
	ChapterTitle *string `json:"chapter_title"`
	ImageCount   int     `json:"image_count"`
	Images       []string `json:"images"`
	// AdminEvidenceImages is the admin's own proof attached to the
	// report's most recent hold — see NovelReport.AdminEvidenceImages.
	// Unlike Images (the reporter's evidence, gated by
	// ShareReporterEvidence for the author-facing endpoint), this is
	// always included once attached.
	AdminEvidenceImages []string   `json:"admin_evidence_images"`
	Status              string     `json:"status"`
	ResolutionNote      string     `json:"resolution_note"`
	AuthorResponse      string     `json:"author_response"`
	ReviewedByName      string     `json:"reviewed_by_name"`
	ReviewedAt          *time.Time `json:"reviewed_at"`
	CreatedAt           time.Time  `json:"created_at"`
	ChapterStatus       *string    `json:"chapter_status"`
	NovelHidden         bool       `json:"novel_hidden"`
	CoverURL            string     `json:"cover_url"`
}

func newReportResponse(report *repository.NovelReport) reportResponse {
	imageURLs := make([]string, 0, len(report.Images))
	for _, image := range report.Images {
		imageURLs = append(imageURLs, image.ImageURL)
	}
	adminEvidenceImages := report.AdminEvidenceImages
	if adminEvidenceImages == nil {
		adminEvidenceImages = []string{}
	}
	return reportResponse{
		ID:                  report.ID,
		NovelID:             report.NovelID,
		NovelTitle:          report.NovelTitle,
		AuthorName:          report.AuthorName,
		OwnerUserID:         report.OwnerUserID,
		UserID:              report.UserID,
		Username:            report.Username,
		Reason:              report.Reason,
		Details:             report.Details,
		ChapterID:           report.ChapterID,
		ChapterTitle:        report.ChapterTitle,
		ImageCount:          report.ImageCount,
		Images:              imageURLs,
		AdminEvidenceImages: adminEvidenceImages,
		Status:              report.Status,
		ResolutionNote:      report.ResolutionNote,
		AuthorResponse:      report.AuthorResponse,
		ReviewedByName:      report.ReviewedByName,
		ReviewedAt:          report.ReviewedAt,
		CreatedAt:           report.CreatedAt,
		ChapterStatus:       report.ChapterStatus,
		NovelHidden:         report.NovelHidden,
		CoverURL:            report.CoverURL,
	}
}

type moderationActionResponse struct {
	ID         string    `json:"id"`
	ReportID   string    `json:"report_id"`
	ActionType string    `json:"action_type"`
	AdminName  string    `json:"admin_name"`
	Notes      string    `json:"notes"`
	CreatedAt  time.Time `json:"created_at"`
	Images     []string  `json:"images"`
}

func newModerationActionResponse(action *repository.ModerationAction) moderationActionResponse {
	images := action.Images
	if images == nil {
		images = []string{}
	}
	return moderationActionResponse{
		ID:         action.ID,
		ReportID:   action.ReportID,
		ActionType: action.ActionType,
		AdminName:  action.AdminName,
		Notes:      action.Notes,
		CreatedAt:  action.CreatedAt,
		Images:     images,
	}
}

// authorModerationActionResponse is moderationActionResponse without
// AdminName — the author's own timeline (see ModerationActionsForOwner)
// shows what happened and when, not which staff account did it.
type authorModerationActionResponse struct {
	ID         string    `json:"id"`
	ActionType string    `json:"action_type"`
	Notes      string    `json:"notes"`
	CreatedAt  time.Time `json:"created_at"`
	Images     []string  `json:"images"`
}

func newAuthorModerationActionResponse(action *repository.ModerationAction) authorModerationActionResponse {
	images := action.Images
	if images == nil {
		images = []string{}
	}
	return authorModerationActionResponse{
		ID:         action.ID,
		ActionType: action.ActionType,
		Notes:      action.Notes,
		CreatedAt:  action.CreatedAt,
		Images:     images,
	}
}

type releaseRequestResponse struct {
	ID           string     `json:"id"`
	ReportID     string     `json:"report_id"`
	Explanation  string     `json:"explanation"`
	Images       []string   `json:"images"`
	Status       string     `json:"status"`
	AdminComment string     `json:"admin_comment"`
	CreatedAt    time.Time  `json:"created_at"`
	ReviewedAt   *time.Time `json:"reviewed_at"`
}

func newReleaseRequestResponse(request *repository.ReleaseRequest) releaseRequestResponse {
	imageURLs := make([]string, 0, len(request.Images))
	for _, image := range request.Images {
		imageURLs = append(imageURLs, image.ImageURL)
	}
	return releaseRequestResponse{
		ID:           request.ID,
		ReportID:     request.ReportID,
		Explanation:  request.Explanation,
		Images:       imageURLs,
		Status:       request.Status,
		AdminComment: request.AdminComment,
		CreatedAt:    request.CreatedAt,
		ReviewedAt:   request.ReviewedAt,
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
// (full record + evidence images). The first time an admin opens a
// still-"submitted" report, this also auto-transitions it to
// under_review (see NovelReportService.GetDetail) — opening the drawer
// is the "admin received the report" moment.
func (handler *NovelReportHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	actorID, _ := actorFromContext(request.Context())
	report, err := handler.reports.GetDetail(request.Context(), request.PathValue("id"), actorID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}

type moderationDecisionRequest struct {
	Notes string `json:"notes"`
}

// Reject: POST /admin/reports/{id}/reject — Action A, the report is invalid.
func (handler *NovelReportHandler) Reject(responseWriter http.ResponseWriter, request *http.Request) {
	var body moderationDecisionRequest
	if !decodeJSON(responseWriter, request, &body) {
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	report, err := handler.reports.Reject(request.Context(), request.PathValue("id"), actorID, body.Notes)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	handler.auditLogger.Log(actorID, actorName, "report.rejected", "report", report.ID, nil)
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}

// ResolveDirect: POST /admin/reports/{id}/resolve — closed as handled, no hold.
func (handler *NovelReportHandler) ResolveDirect(responseWriter http.ResponseWriter, request *http.Request) {
	var body moderationDecisionRequest
	if !decodeJSON(responseWriter, request, &body) {
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	report, err := handler.reports.ResolveDirect(request.Context(), request.PathValue("id"), actorID, body.Notes)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	handler.auditLogger.Log(actorID, actorName, "report.resolved_direct", "report", report.ID, nil)
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}

// HoldChapter: POST /admin/reports/{id}/hold-chapter — Action B.
// Multipart form: notes, share_reporter_evidence ("true"/"false"),
// up to 3 files under "images" — the admin's own evidence, separate
// from the reporter's (see NovelReportService.HoldChapter).
func (handler *NovelReportHandler) HoldChapter(responseWriter http.ResponseWriter, request *http.Request) {
	imageURLs, err := saveUploadedReportImages(
		request, "images", handler.holdImagesDir, handler.holdImagesPrefix, uuid.NewString())
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}

	notes := request.FormValue("notes")
	shareReporterEvidence := request.FormValue("share_reporter_evidence") == "true"

	actorID, actorName := actorFromContext(request.Context())
	report, err := handler.reports.HoldChapter(
		request.Context(), request.PathValue("id"), actorID, notes, shareReporterEvidence, imageURLs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	handler.auditLogger.Log(actorID, actorName, "report.hold_chapter", "report", report.ID,
		map[string]any{"chapter_id": report.ChapterID})
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}

// HoldNovel: POST /admin/reports/{id}/hold-novel — Action C. Same
// multipart shape as HoldChapter.
func (handler *NovelReportHandler) HoldNovel(responseWriter http.ResponseWriter, request *http.Request) {
	imageURLs, err := saveUploadedReportImages(
		request, "images", handler.holdImagesDir, handler.holdImagesPrefix, uuid.NewString())
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}

	notes := request.FormValue("notes")
	shareReporterEvidence := request.FormValue("share_reporter_evidence") == "true"

	actorID, actorName := actorFromContext(request.Context())
	report, err := handler.reports.HoldNovel(
		request.Context(), request.PathValue("id"), actorID, notes, shareReporterEvidence, imageURLs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	handler.auditLogger.Log(actorID, actorName, "report.hold_novel", "report", report.ID,
		map[string]any{"novel_id": report.NovelID})
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}

// ApproveRelease: POST /admin/reports/{id}/release-requests/{requestId}/approve
// — multipart form (notes, up to 3 files under "images" — the admin's
// own proof for approving, same optional-evidence shape HoldChapter/
// HoldNovel already have).
func (handler *NovelReportHandler) ApproveRelease(responseWriter http.ResponseWriter, request *http.Request) {
	imageURLs, err := saveUploadedReportImages(
		request, "images", handler.holdImagesDir, handler.holdImagesPrefix, uuid.NewString())
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}

	notes := request.FormValue("notes")
	actorID, actorName := actorFromContext(request.Context())
	report, err := handler.reports.ApproveRelease(
		request.Context(), request.PathValue("id"), request.PathValue("requestId"), actorID, notes, imageURLs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	handler.auditLogger.Log(actorID, actorName, "report.approve_release", "report", report.ID, nil)
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}

// RejectRelease: POST /admin/reports/{id}/release-requests/{requestId}/reject
// — multipart form (comment, up to 3 files under "images"), same shape as ApproveRelease.
func (handler *NovelReportHandler) RejectRelease(responseWriter http.ResponseWriter, request *http.Request) {
	imageURLs, err := saveUploadedReportImages(
		request, "images", handler.holdImagesDir, handler.holdImagesPrefix, uuid.NewString())
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}

	comment := request.FormValue("comment")
	actorID, actorName := actorFromContext(request.Context())
	report, err := handler.reports.RejectRelease(
		request.Context(), request.PathValue("id"), request.PathValue("requestId"), actorID, comment, imageURLs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	handler.auditLogger.Log(actorID, actorName, "report.reject_release", "report", report.ID, nil)
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}

// ModerationActions: GET /admin/reports/{id}/moderation-actions — the
// drawer's History timeline.
func (handler *NovelReportHandler) ModerationActions(responseWriter http.ResponseWriter, request *http.Request) {
	actions, err := handler.reports.ModerationActions(request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]moderationActionResponse, 0, len(actions))
	for _, action := range actions {
		items = append(items, newModerationActionResponse(action))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

// AdminDelete: DELETE /admin/reports/{id} — soft delete, refused while
// the report is actively holding content (see NovelReportService.
// AdminDelete). A background ticker hard-deletes it 30 days later.
func (handler *NovelReportHandler) AdminDelete(responseWriter http.ResponseWriter, request *http.Request) {
	if err := handler.reports.AdminDelete(request.Context(), request.PathValue("id")); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	handler.auditLogger.Log(actorID, actorName, "report.deleted", "report", request.PathValue("id"), nil)
	responseWriter.WriteHeader(http.StatusNoContent)
}

// AdminBulkDelete: DELETE /admin/reports/bulk?status=resolved|rejected
// — clears an entire terminal status at once, so decluttering an old
// backlog doesn't mean deleting one row at a time.
func (handler *NovelReportHandler) AdminBulkDelete(responseWriter http.ResponseWriter, request *http.Request) {
	status := request.URL.Query().Get("status")
	deletedCount, err := handler.reports.AdminBulkDelete(request.Context(), status)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	handler.auditLogger.Log(actorID, actorName, "report.bulk_deleted", "report", "",
		map[string]any{"status": status, "deleted_count": deletedCount})
	writeJSON(responseWriter, http.StatusOK, map[string]any{"deleted_count": deletedCount})
}

// ReleaseRequestsAdmin: GET /admin/reports/{id}/release-requests
func (handler *NovelReportHandler) ReleaseRequestsAdmin(responseWriter http.ResponseWriter, request *http.Request) {
	requests, err := handler.reports.ReleaseRequests(request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]releaseRequestResponse, 0, len(requests))
	for _, req := range requests {
		items = append(items, newReleaseRequestResponse(req))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
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

// ListForOwner: GET /author/reports?status=&page=&page_size= — the
// author dashboard's Reports page. Requires the RequireAuthor
// middleware.
func (handler *NovelReportHandler) ListForOwner(responseWriter http.ResponseWriter, request *http.Request) {
	ownerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	reports, total, err := handler.reports.ListForOwner(request.Context(), ownerUserID, query.Get("status"), page, pageSize)
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

// ReleaseRequestsForOwner: GET /author/reports/{id}/release-requests —
// the author's own report's release-request history (so a rejected
// request's admin_comment stays visible before they try again).
// Requires the RequireAuthor middleware.
func (handler *NovelReportHandler) ReleaseRequestsForOwner(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	requests, err := handler.reports.ReleaseRequestsForOwner(request.Context(), request.PathValue("id"), callerUserID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]releaseRequestResponse, 0, len(requests))
	for _, req := range requests {
		items = append(items, newReleaseRequestResponse(req))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

// ModerationActionsForOwner: GET /author/reports/{id}/moderation-actions
// — the author dashboard's own report timeline. Requires the
// RequireAuthor middleware.
func (handler *NovelReportHandler) ModerationActionsForOwner(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	actions, err := handler.reports.ModerationActionsForOwner(request.Context(), request.PathValue("id"), callerUserID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]authorModerationActionResponse, 0, len(actions))
	for _, action := range actions {
		items = append(items, newAuthorModerationActionResponse(action))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

// SubmitReleaseRequest: POST /author/reports/{id}/release-requests —
// multipart form (explanation, up to 3 files under "images"). The
// author's side of a hold: they've fixed what the hold note asked for
// and want it looked at again. Requires the RequireAuthor middleware.
func (handler *NovelReportHandler) SubmitReleaseRequest(responseWriter http.ResponseWriter, request *http.Request) {
	callerUserID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	imageURLs, err := saveUploadedReportImages(
		request, "images", handler.releaseImagesDir, handler.releaseImagesPrefix, uuid.NewString())
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}

	explanation := request.FormValue("explanation")
	report, err := handler.reports.SubmitReleaseRequest(
		request.Context(), request.PathValue("id"), callerUserID, explanation, imageURLs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newReportResponse(report))
}
