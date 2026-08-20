package handler

import (
	"net/http"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const (
	maxReportReasonTypeLabelLength       = 100
	maxReportReasonTypeDescriptionLength = 500
)

// ReportReasonTypeHandler serves report-reason-*type* CRUD — the
// broader categories (Content Policy, Copyright, ...) each
// ReportReason belongs to. Admin-only; unlike ReportReasonHandler
// there's no public mount, because readers never see a type directly
// — it's what the author dashboard shows on a report instead of (or
// alongside) the reader's specific reason, sourced from the
// novel_reports.reason_type snapshot rather than a live fetch here.
type ReportReasonTypeHandler struct {
	types       *repository.ReportReasonTypeRepository
	auditLogger *audit.Logger
}

func NewReportReasonTypeHandler(types *repository.ReportReasonTypeRepository, auditLogger *audit.Logger) *ReportReasonTypeHandler {
	return &ReportReasonTypeHandler{types: types, auditLogger: auditLogger}
}

type reportReasonTypeResponse struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Position    int    `json:"position"`
}

func newReportReasonTypeResponse(reasonType *repository.ReportReasonType) reportReasonTypeResponse {
	return reportReasonTypeResponse{
		ID:          reasonType.ID,
		Label:       reasonType.Label,
		Description: reasonType.Description,
		Position:    reasonType.Position,
	}
}

// List: GET /admin/report-reason-types
func (typeHandler *ReportReasonTypeHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	types, err := typeHandler.types.List(request.Context())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]reportReasonTypeResponse, 0, len(types))
	for _, reasonType := range types {
		items = append(items, newReportReasonTypeResponse(reasonType))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

type reportReasonTypeWriteRequest struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

func (typeHandler *ReportReasonTypeHandler) validateWrite(writeRequest reportReasonTypeWriteRequest) (label, description string, ok bool) {
	label = strings.TrimSpace(writeRequest.Label)
	description = strings.TrimSpace(writeRequest.Description)
	if label == "" || len(label) > maxReportReasonTypeLabelLength {
		return "", "", false
	}
	if len(description) > maxReportReasonTypeDescriptionLength {
		return "", "", false
	}
	return label, description, true
}

// Create: POST /admin/report-reason-types
func (typeHandler *ReportReasonTypeHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest reportReasonTypeWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	label, description, ok := typeHandler.validateWrite(writeRequest)
	if !ok {
		writeError(responseWriter, http.StatusBadRequest,
			"label is required (max 100 characters); description max 500 characters")
		return
	}

	reasonType, err := typeHandler.types.Create(request.Context(), label, description)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	typeHandler.auditLogger.Log(actorID, actorName, "report_reason_type.created", "report_reason_type", reasonType.ID,
		map[string]any{"label": reasonType.Label})
	writeJSON(responseWriter, http.StatusCreated, newReportReasonTypeResponse(reasonType))
}

// Update: PUT /admin/report-reason-types/{id} — relabel/redescribe in
// place. Reports already submitted under it keep the old text
// verbatim (see migration 0041's comment).
func (typeHandler *ReportReasonTypeHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest reportReasonTypeWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	label, description, ok := typeHandler.validateWrite(writeRequest)
	if !ok {
		writeError(responseWriter, http.StatusBadRequest,
			"label is required (max 100 characters); description max 500 characters")
		return
	}

	reasonType, err := typeHandler.types.Update(request.Context(), request.PathValue("id"), label, description)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	typeHandler.auditLogger.Log(actorID, actorName, "report_reason_type.updated", "report_reason_type", reasonType.ID,
		map[string]any{"label": reasonType.Label})
	writeJSON(responseWriter, http.StatusOK, newReportReasonTypeResponse(reasonType))
}

// Delete: DELETE /admin/report-reason-types/{id} — 409s while any
// report_reasons row still belongs to it.
func (typeHandler *ReportReasonTypeHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	typeID := request.PathValue("id")
	if err := typeHandler.types.Delete(request.Context(), typeID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	typeHandler.auditLogger.Log(actorID, actorName, "report_reason_type.deleted", "report_reason_type", typeID, nil)
	responseWriter.WriteHeader(http.StatusNoContent)
}

type reportReasonTypeReorderRequest struct {
	IDs []string `json:"ids"`
}

// Reorder: PUT /admin/report-reason-types/reorder — drag-reorder.
func (typeHandler *ReportReasonTypeHandler) Reorder(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest reportReasonTypeReorderRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	if len(writeRequest.IDs) == 0 {
		writeError(responseWriter, http.StatusBadRequest, "ids is required")
		return
	}

	if err := typeHandler.types.Reorder(request.Context(), writeRequest.IDs); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	typeHandler.auditLogger.Log(actorID, actorName, "report_reason_type.reordered", "report_reason_type", "",
		map[string]any{"ids": writeRequest.IDs})
	responseWriter.WriteHeader(http.StatusNoContent)
}
