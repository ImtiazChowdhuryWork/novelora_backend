package handler

import (
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

type AdminNovelHandler struct {
	novelService    *service.NovelService
	coversDirectory string
	coversUrlPrefix string
}

func NewAdminNovelHandler(novelService *service.NovelService, uploadsDirectory string) *AdminNovelHandler {
	return &AdminNovelHandler{
		novelService:    novelService,
		coversDirectory: filepath.Join(uploadsDirectory, "covers"),
		coversUrlPrefix: "/uploads/covers/",
	}
}

type novelWriteRequest struct {
	Title         string   `json:"title"`
	AuthorName    string   `json:"author_name"`
	Synopsis      string   `json:"synopsis"`
	Status        string   `json:"status"`
	IsShort       bool     `json:"is_short"`
	IsRecommended bool     `json:"is_recommended"`
	Rating        *float64 `json:"rating"`
	GenreIDs      []string `json:"genre_ids"`
}

func (writeRequest novelWriteRequest) toWrite() repository.NovelWrite {
	return repository.NovelWrite{
		Title:         writeRequest.Title,
		AuthorName:    writeRequest.AuthorName,
		Synopsis:      writeRequest.Synopsis,
		Status:        writeRequest.Status,
		IsShort:       writeRequest.IsShort,
		IsRecommended: writeRequest.IsRecommended,
		Rating:        writeRequest.Rating,
	}
}

type genreResponse struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type novelResponse struct {
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	AuthorName        string          `json:"author_name"`
	Synopsis          string          `json:"synopsis"`
	CoverURL          string          `json:"cover_url"`
	Status            string          `json:"status"`
	IsShort           bool            `json:"is_short"`
	IsRecommended     bool            `json:"is_recommended"`
	Rating            *float64        `json:"rating"`
	ViewCount         int64           `json:"view_count"`
	PublishedChapters int             `json:"published_chapters"`
	TotalChapters     int             `json:"total_chapters"`
	Genres            []genreResponse `json:"genres"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

func newNovelResponse(novel *repository.Novel) novelResponse {
	genres := make([]genreResponse, 0, len(novel.Genres))
	for _, genre := range novel.Genres {
		genres = append(genres, genreResponse{ID: genre.ID, Name: genre.Name})
	}
	return novelResponse{
		ID:                novel.ID,
		Title:             novel.Title,
		AuthorName:        novel.AuthorName,
		Synopsis:          novel.Synopsis,
		CoverURL:          novel.CoverURL,
		Status:            novel.Status,
		IsShort:           novel.IsShort,
		IsRecommended:     novel.IsRecommended,
		Rating:            novel.Rating,
		ViewCount:         novel.ViewCount,
		PublishedChapters: novel.PublishedChapters,
		TotalChapters:     novel.TotalChapters,
		Genres:            genres,
		CreatedAt:         novel.CreatedAt,
		UpdatedAt:         novel.UpdatedAt,
	}
}

// List returns a page of novels: GET /admin/novels?page=&page_size=&search=&status=
func (adminNovelHandler *AdminNovelHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	filter := repository.NovelListFilter{
		Search:   query.Get("search"),
		Status:   query.Get("status"),
		Page:     page,
		PageSize: pageSize,
	}

	novels, total, err := adminNovelHandler.novelService.List(request.Context(), filter)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	items := make([]novelResponse, 0, len(novels))
	for _, novel := range novels {
		items = append(items, newNovelResponse(novel))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"items": items,
		"total": total,
	})
}

func (adminNovelHandler *AdminNovelHandler) Get(responseWriter http.ResponseWriter, request *http.Request) {
	novel, err := adminNovelHandler.novelService.Get(request.Context(), request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}

func (adminNovelHandler *AdminNovelHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest novelWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	novel, err := adminNovelHandler.novelService.Create(request.Context(), writeRequest.toWrite(), writeRequest.GenreIDs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusCreated, newNovelResponse(novel))
}

func (adminNovelHandler *AdminNovelHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	var writeRequest novelWriteRequest
	if !decodeJSON(responseWriter, request, &writeRequest) {
		return
	}
	novel, err := adminNovelHandler.novelService.Update(
		request.Context(), request.PathValue("id"), writeRequest.toWrite(), writeRequest.GenreIDs)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}

func (adminNovelHandler *AdminNovelHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	if err := adminNovelHandler.novelService.Delete(request.Context(), request.PathValue("id")); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}

// UpdateCover stores an uploaded image (multipart field "cover").
func (adminNovelHandler *AdminNovelHandler) UpdateCover(responseWriter http.ResponseWriter, request *http.Request) {
	novelID := request.PathValue("id")

	coverURL, err := saveUploadedImage(
		request, "cover", adminNovelHandler.coversDirectory,
		adminNovelHandler.coversUrlPrefix, novelID)
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}

	if err := adminNovelHandler.novelService.UpdateCover(request.Context(), novelID, coverURL); err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	novel, err := adminNovelHandler.novelService.Get(request.Context(), novelID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newNovelResponse(novel))
}
