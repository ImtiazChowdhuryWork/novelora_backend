package handler

import (
	"net/http"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const maxReportReasonLabelLength = 100

// ReportReasonHandler serves report-reason-type CRUD — the admin-
// managed menu of choices Book Detail's flag-icon "Report this novel"
// sheet presents. List is registered both under /admin/report-reasons
// (dashboard) and /report-reasons (public — the app's report sheet
// reads it); Create/Update/Delete are admin-only. Mirrors
// GenreHandler's shape.
type ReportReasonHandler struct {
	reasons     *repository.ReportReasonRepository
	auditLogger *audit.Logger
}

func NewReportReasonHandler(reasons *repository.ReportReasonRepository, auditLogger *audit.Logger) *ReportReasonHandler {
	return &ReportReasonHandler{reasons: reasons, auditLogger: auditLogger}
}

type reportReasonResponse struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	RequiresDetails bool   `json:"requires_details"`
	Position        int    `json:"position"`
	TypeID          string `json:"type_id"`
}

func newReportReasonResponse(reason *repository.ReportReason) reportReasonResponse {
	return reportReasonResponse{
		ID:              reason.ID,
		Label:           reason.Label,
		RequiresDetails: reason.RequiresDetails,
		Position:        reason.Position,
		TypeID:          reason.TypeID,
	}
}

// List: GET /report-reasons or /admin/report-reasons
func (reasonHandler *ReportReasonHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	reasons, err := reasonHandler.reasons.List(request.Context())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]reportReasonResponse, 0, len(reasons))
	for _, reason := range reasons {
		items = append(items, newReportReasonResponse(reason))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

type reportReasonWriteRequest struct {
	Label           string `json:"label"`
	RequiresDetails bool   `json:"requires_details"`
	TypeID          string `json:"type_id"`
}

// Create: POST /admin/report-reasons
func (reasonHandler *ReportReasonHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest reportReasonWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	label := strings.TrimSpace(writeRequest.Label)
	if label == "" || len(label) > maxReportReasonLabelLength {
		writeError(responseWriter, http.StatusBadRequest, "label is required (max 100 characters)")
		return
	}
	if writeRequest.TypeID == "" {
		writeError(responseWriter, http.StatusBadRequest, "type_id is required")
		return
	}

	reason, err := reasonHandler.reasons.Create(request.Context(), label, writeRequest.RequiresDetails, writeRequest.TypeID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	reasonHandler.auditLogger.Log(actorID, actorName, "report_reason.created", "report_reason", reason.ID,
		map[string]any{"label": reason.Label, "type_id": reason.TypeID})
	writeJSON(responseWriter, http.StatusCreated, newReportReasonResponse(reason))
}

// Update: PUT /admin/report-reasons/{id} — relabel, retoggle
// requires_details, and/or move to a different type, in place.
// Reports already submitted under the old label/type keep it verbatim
// (reason/reason_type are stored as free text, not a foreign key —
// see migrations 0037 and 0041).
func (reasonHandler *ReportReasonHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest reportReasonWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	label := strings.TrimSpace(writeRequest.Label)
	if label == "" || len(label) > maxReportReasonLabelLength {
		writeError(responseWriter, http.StatusBadRequest, "label is required (max 100 characters)")
		return
	}
	if writeRequest.TypeID == "" {
		writeError(responseWriter, http.StatusBadRequest, "type_id is required")
		return
	}

	reason, err := reasonHandler.reasons.Update(
		request.Context(), request.PathValue("id"), label, writeRequest.RequiresDetails, writeRequest.TypeID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	reasonHandler.auditLogger.Log(actorID, actorName, "report_reason.updated", "report_reason", reason.ID,
		map[string]any{"label": reason.Label, "requires_details": reason.RequiresDetails, "type_id": reason.TypeID})
	writeJSON(responseWriter, http.StatusOK, newReportReasonResponse(reason))
}

// Delete: DELETE /admin/report-reasons/{id} — 409s if it's the last
// remaining reason type, so the app's report sheet never ends up with
// zero choices.
func (reasonHandler *ReportReasonHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	reasonID := request.PathValue("id")
	if err := reasonHandler.reasons.Delete(request.Context(), reasonID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	reasonHandler.auditLogger.Log(actorID, actorName, "report_reason.deleted", "report_reason", reasonID, nil)
	responseWriter.WriteHeader(http.StatusNoContent)
}

type reportReasonReorderRequest struct {
	IDs []string `json:"ids"`
}

// Reorder: PUT /admin/report-reasons/reorder — drag-reorder in the
// dashboard's Report Types list. Body is the full id list in the
// desired display order.
func (reasonHandler *ReportReasonHandler) Reorder(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest reportReasonReorderRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	if len(writeRequest.IDs) == 0 {
		writeError(responseWriter, http.StatusBadRequest, "ids is required")
		return
	}

	if err := reasonHandler.reasons.Reorder(request.Context(), writeRequest.IDs); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	reasonHandler.auditLogger.Log(actorID, actorName, "report_reason.reordered", "report_reason", "",
		map[string]any{"ids": writeRequest.IDs})
	responseWriter.WriteHeader(http.StatusNoContent)
}
