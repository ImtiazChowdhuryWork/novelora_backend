package handler

import (
	"net/http"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// GenreHandler serves genre CRUD. List is registered both under
// /admin/genres (dashboard) and /genres (public — so a future
// Discover-by-genre feature in the app has something to read); it's a
// harmless, side-effect-free read either way. Create/Delete are
// admin-only.
type GenreHandler struct {
	genres      *repository.GenreRepository
	auditLogger *audit.Logger
}

func NewGenreHandler(genres *repository.GenreRepository, auditLogger *audit.Logger) *GenreHandler {
	return &GenreHandler{genres: genres, auditLogger: auditLogger}
}

func newGenreItemResponse(genre *repository.Genre) genreResponse {
	return genreResponse{ID: genre.ID, Name: genre.Name, Kind: genre.Kind}
}

// List: GET /genres or /admin/genres
func (genreHandler *GenreHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	genres, err := genreHandler.genres.List(request.Context())
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]genreResponse, 0, len(genres))
	for _, genre := range genres {
		items = append(items, newGenreItemResponse(genre))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

type genreWriteRequest struct {
	Name string `json:"name"`
	// "genre" (a broad category, e.g. Romance, Mafia) or "tag" (a trope
	// within one, e.g. Alpha, Revenge). Defaults to "genre".
	Kind string `json:"kind"`
}

// Create: POST /admin/genres
func (genreHandler *GenreHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest genreWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	name := strings.TrimSpace(writeRequest.Name)
	if name == "" || len(name) > 80 {
		writeError(responseWriter, http.StatusBadRequest, "name is required (max 80 characters)")
		return
	}
	kind := writeRequest.Kind
	if kind == "" {
		kind = "genre"
	}
	if kind != "genre" && kind != "tag" {
		writeError(responseWriter, http.StatusBadRequest, "kind must be 'genre' or 'tag'")
		return
	}

	genre, err := genreHandler.genres.Create(request.Context(), name, kind)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	genreHandler.auditLogger.Log(actorID, actorName, "genre.created", "genre", genre.ID,
		map[string]any{"name": genre.Name})
	writeJSON(responseWriter, http.StatusCreated, newGenreItemResponse(genre))
}

// Update: PUT /admin/genres/{id} — rename and/or reclassify in place,
// keeping every novel already assigned to it assigned.
func (genreHandler *GenreHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest genreWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	name := strings.TrimSpace(writeRequest.Name)
	if name == "" || len(name) > 80 {
		writeError(responseWriter, http.StatusBadRequest, "name is required (max 80 characters)")
		return
	}
	kind := writeRequest.Kind
	if kind == "" {
		kind = "genre"
	}
	if kind != "genre" && kind != "tag" {
		writeError(responseWriter, http.StatusBadRequest, "kind must be 'genre' or 'tag'")
		return
	}

	genre, err := genreHandler.genres.Update(request.Context(), request.PathValue("id"), name, kind)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	genreHandler.auditLogger.Log(actorID, actorName, "genre.updated", "genre", genre.ID,
		map[string]any{"name": genre.Name, "kind": genre.Kind})
	writeJSON(responseWriter, http.StatusOK, newGenreItemResponse(genre))
}

// Delete: DELETE /admin/genres/{id}
func (genreHandler *GenreHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	genreID := request.PathValue("id")
	if err := genreHandler.genres.Delete(request.Context(), genreID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	genreHandler.auditLogger.Log(actorID, actorName, "genre.deleted", "genre", genreID, nil)
	responseWriter.WriteHeader(http.StatusNoContent)
}
