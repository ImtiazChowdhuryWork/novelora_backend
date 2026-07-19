package handler

import (
	"net/http"
	"strings"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// GenreHandler serves genre CRUD. List is registered both under
// /admin/genres (dashboard) and /genres (public — so a future
// Discover-by-genre feature in the app has something to read); it's a
// harmless, side-effect-free read either way. Create/Delete are
// admin-only.
type GenreHandler struct {
	genres *repository.GenreRepository
}

func NewGenreHandler(genres *repository.GenreRepository) *GenreHandler {
	return &GenreHandler{genres: genres}
}

func newGenreItemResponse(genre *repository.Genre) genreResponse {
	return genreResponse{ID: genre.ID, Name: genre.Name}
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

	genre, err := genreHandler.genres.Create(request.Context(), name)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusCreated, newGenreItemResponse(genre))
}

// Delete: DELETE /admin/genres/{id}
func (genreHandler *GenreHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	if err := genreHandler.genres.Delete(request.Context(), request.PathValue("id")); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}
