package handler

import (
	"net/http"
	"strconv"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

// DiscoverSectionHandler serves the Section Registry: admin CRUD over
// discover_sections + novel_section_overrides, plus the public read the
// app will eventually fetch its Discover section list from. See
// DiscoverSectionService.Resolve for where a section's rows actually
// turn into novels.
type DiscoverSectionHandler struct {
	sections    *service.DiscoverSectionService
	auditLogger *audit.Logger
}

func NewDiscoverSectionHandler(sections *service.DiscoverSectionService, auditLogger *audit.Logger) *DiscoverSectionHandler {
	return &DiscoverSectionHandler{sections: sections, auditLogger: auditLogger}
}

type discoverSectionResponse struct {
	Key                string   `json:"key"`
	Category           string   `json:"category"`
	Label              string   `json:"label"`
	Layout             string   `json:"layout"`
	Scope              string   `json:"scope"`
	GenreNames         []string `json:"genre_names"`
	Sort               string   `json:"sort"`
	StatusFilter       string   `json:"status_filter"`
	RecommendedFilter  *bool    `json:"recommended_filter"`
	ExclusiveFilter    *bool    `json:"exclusive_filter"`
	ExcludeSectionKeys []string `json:"exclude_section_keys"`
	Position           int      `json:"position"`
	Active             bool     `json:"active"`
}

func newDiscoverSectionResponse(section *repository.DiscoverSection) discoverSectionResponse {
	return discoverSectionResponse{
		Key:                section.Key,
		Category:           section.Category,
		Label:              section.Label,
		Layout:             section.Layout,
		Scope:              section.Scope,
		GenreNames:         section.GenreNames,
		Sort:               section.Sort,
		StatusFilter:       section.StatusFilter,
		RecommendedFilter:  section.RecommendedFilter,
		ExclusiveFilter:    section.ExclusiveFilter,
		ExcludeSectionKeys: section.ExcludeSectionKeys,
		Position:           section.Position,
		Active:             section.Active,
	}
}

// List: GET /admin/discover-sections (every section) or
// GET /discover-sections (active only — the public/app-facing read).
func (handler *DiscoverSectionHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	sections, err := handler.sections.List(request.Context())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": mapDiscoverSections(sections)})
}

// ListActive: GET /discover-sections — public, active-only.
func (handler *DiscoverSectionHandler) ListActive(responseWriter http.ResponseWriter, request *http.Request) {
	sections, err := handler.sections.ListActive(request.Context())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": mapDiscoverSections(sections)})
}

func mapDiscoverSections(sections []*repository.DiscoverSection) []discoverSectionResponse {
	items := make([]discoverSectionResponse, 0, len(sections))
	for _, section := range sections {
		items = append(items, newDiscoverSectionResponse(section))
	}
	return items
}

type discoverSectionWriteRequest struct {
	Key                string   `json:"key"`
	Category           string   `json:"category"`
	Label              string   `json:"label"`
	Layout             string   `json:"layout"`
	Scope              string   `json:"scope"`
	GenreNames         []string `json:"genre_names"`
	Sort               string   `json:"sort"`
	StatusFilter       string   `json:"status_filter"`
	RecommendedFilter  *bool    `json:"recommended_filter"`
	ExclusiveFilter    *bool    `json:"exclusive_filter"`
	ExcludeSectionKeys []string `json:"exclude_section_keys"`
	Position           int      `json:"position"`
	Active             bool     `json:"active"`
}

func (writeRequest discoverSectionWriteRequest) toWrite() repository.DiscoverSectionWrite {
	return repository.DiscoverSectionWrite{
		Category:           writeRequest.Category,
		Label:              writeRequest.Label,
		Layout:             writeRequest.Layout,
		Scope:              writeRequest.Scope,
		GenreNames:         writeRequest.GenreNames,
		Sort:               writeRequest.Sort,
		StatusFilter:       writeRequest.StatusFilter,
		RecommendedFilter:  writeRequest.RecommendedFilter,
		ExclusiveFilter:    writeRequest.ExclusiveFilter,
		ExcludeSectionKeys: writeRequest.ExcludeSectionKeys,
		Position:           writeRequest.Position,
		Active:             writeRequest.Active,
	}
}

// Create: POST /admin/discover-sections
func (handler *DiscoverSectionHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest discoverSectionWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	section, err := handler.sections.Create(request.Context(), writeRequest.Key, writeRequest.toWrite())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	handler.auditLogger.Log(actorID, actorName, "discover_section.created", "discover_section", section.Key,
		map[string]any{"label": section.Label})
	writeJSON(responseWriter, http.StatusCreated, newDiscoverSectionResponse(section))
}

// Update: PUT /admin/discover-sections/{key}
func (handler *DiscoverSectionHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest discoverSectionWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	section, err := handler.sections.Update(request.Context(), request.PathValue("key"), writeRequest.toWrite())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	handler.auditLogger.Log(actorID, actorName, "discover_section.updated", "discover_section", section.Key,
		map[string]any{"label": section.Label})
	writeJSON(responseWriter, http.StatusOK, newDiscoverSectionResponse(section))
}

// Delete: DELETE /admin/discover-sections/{key}
func (handler *DiscoverSectionHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	key := request.PathValue("key")
	if err := handler.sections.Delete(request.Context(), key); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	handler.auditLogger.Log(actorID, actorName, "discover_section.deleted", "discover_section", key, nil)
	responseWriter.WriteHeader(http.StatusNoContent)
}

type overrideResponse struct {
	SectionKey string `json:"section_key"`
	NovelID    string `json:"novel_id"`
	Type       string `json:"type"`
	Position   *int   `json:"position"`
}

// ListOverrides: GET /admin/discover-sections/{key}/overrides
func (handler *DiscoverSectionHandler) ListOverrides(responseWriter http.ResponseWriter, request *http.Request) {
	overrides, err := handler.sections.ListOverrides(request.Context(), request.PathValue("key"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]overrideResponse, 0, len(overrides))
	for _, override := range overrides {
		items = append(items, overrideResponse{
			SectionKey: override.SectionKey, NovelID: override.NovelID,
			Type: override.Type, Position: override.Position,
		})
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

type overrideWriteRequest struct {
	Type     string `json:"type"`
	Position *int   `json:"position"`
}

// SetOverride: PUT /admin/discover-sections/{key}/overrides/{novelId}
func (handler *DiscoverSectionHandler) SetOverride(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest overrideWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	sectionKey := request.PathValue("key")
	novelID := request.PathValue("novelId")
	if err := handler.sections.SetOverride(request.Context(), sectionKey, novelID, writeRequest.Type, writeRequest.Position); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	handler.auditLogger.Log(actorID, actorName, "novel_section_override.set", "discover_section", sectionKey,
		map[string]any{"novel_id": novelID, "type": writeRequest.Type})
	responseWriter.WriteHeader(http.StatusNoContent)
}

// RemoveOverride: DELETE /admin/discover-sections/{key}/overrides/{novelId}
func (handler *DiscoverSectionHandler) RemoveOverride(responseWriter http.ResponseWriter, request *http.Request) {
	sectionKey := request.PathValue("key")
	novelID := request.PathValue("novelId")
	if err := handler.sections.RemoveOverride(request.Context(), sectionKey, novelID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	handler.auditLogger.Log(actorID, actorName, "novel_section_override.removed", "discover_section", sectionKey,
		map[string]any{"novel_id": novelID})
	responseWriter.WriteHeader(http.StatusNoContent)
}

const defaultSectionNovelsPageSize = 12

// Novels: GET /discover-sections/{key}/novels?page_size=N&page=N — public,
// reader-facing resolve (algorithmic fill + admin overrides applied).
// This backs both the shelf preview (page omitted/1, small page_size) and
// the app's "More" list for every registry-driven section (larger
// page_size, page 2+ as the reader scrolls) — one resolved list, same
// pins/excludes applied, wherever a section's novels are shown. No admin
// auth required — same tier as GET /novels.
func (handler *DiscoverSectionHandler) Novels(responseWriter http.ResponseWriter, request *http.Request) {
	pageSize, err := strconv.Atoi(request.URL.Query().Get("page_size"))
	if err != nil || pageSize <= 0 {
		pageSize = defaultSectionNovelsPageSize
	}
	page, err := strconv.Atoi(request.URL.Query().Get("page"))
	if err != nil || page <= 0 {
		page = 1
	}
	novels, err := handler.sections.Resolve(request.Context(), request.PathValue("key"), page, pageSize)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]novelResponse, 0, len(novels))
	for _, novel := range novels {
		items = append(items, newNovelResponse(novel))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

const previewLimit = 20

// Preview: GET /admin/discover-sections/{key}/preview — the actual
// resolved novel list (algorithmic fill + overrides applied), so an
// admin editing a section or an override can see the real effect
// immediately instead of having to open the app.
func (handler *DiscoverSectionHandler) Preview(responseWriter http.ResponseWriter, request *http.Request) {
	novels, err := handler.sections.Resolve(request.Context(), request.PathValue("key"), 1, previewLimit)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]novelResponse, 0, len(novels))
	for _, novel := range novels {
		items = append(items, newNovelResponse(novel))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

type suggestRequest struct {
	GenreNames    []string `json:"genre_names"`
	Status        string   `json:"status"`
	IsRecommended bool     `json:"is_recommended"`
	IsExclusive   bool     `json:"is_exclusive"`
}

type suggestionResponse struct {
	Key      string `json:"key"`
	Category string `json:"category"`
	Label    string `json:"label"`
	Reason   string `json:"reason"`
}

// Suggest: POST /admin/discover-sections/suggest — the dashboard's
// "which sections would this novel currently qualify for" advisor.
// Works against an in-progress, not-yet-saved novel (the create form's
// current field values) just as well as an existing one, since it never
// needs a novel id — see DiscoverSectionService.Suggest.
func (handler *DiscoverSectionHandler) Suggest(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest suggestRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	suggestions, err := handler.sections.Suggest(request.Context(), service.SuggestionCriteria{
		GenreNames:    writeRequest.GenreNames,
		Status:        writeRequest.Status,
		IsRecommended: writeRequest.IsRecommended,
		IsExclusive:   writeRequest.IsExclusive,
	})
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]suggestionResponse, 0, len(suggestions))
	for _, suggestion := range suggestions {
		items = append(items, suggestionResponse{
			Key: suggestion.Key, Category: suggestion.Category,
			Label: suggestion.Label, Reason: suggestion.Reason,
		})
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

// NovelOverrides: GET /admin/novels/{id}/section-overrides — every
// section a specific (already-saved) novel currently has a manual
// pin/exclude override in. The other half of the same dashboard panel
// Suggest backs — this one only makes sense for a novel that already
// has an id, unlike Suggest.
func (handler *DiscoverSectionHandler) NovelOverrides(responseWriter http.ResponseWriter, request *http.Request) {
	overrides, err := handler.sections.ListOverridesForNovel(request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]overrideResponse, 0, len(overrides))
	for _, override := range overrides {
		items = append(items, overrideResponse{
			SectionKey: override.SectionKey, NovelID: override.NovelID,
			Type: override.Type, Position: override.Position,
		})
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}
