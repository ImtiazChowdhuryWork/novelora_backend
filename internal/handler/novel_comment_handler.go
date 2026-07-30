package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

// NovelCommentHandler serves Phase 5c's novel-level comments. List is
// public (guests can read) but reads an optional Bearer token so each
// comment's is_own reflects the viewer, same optional-auth shape as
// PublicNovelHandler.Get's my_rating.
type NovelCommentHandler struct {
	comments    *service.NovelCommentService
	auditLogger *audit.Logger
	jwtSecret   []byte
}

func NewNovelCommentHandler(comments *service.NovelCommentService, auditLogger *audit.Logger, jwtSecret []byte) *NovelCommentHandler {
	return &NovelCommentHandler{comments: comments, auditLogger: auditLogger, jwtSecret: jwtSecret}
}

type commentResponse struct {
	ID              string            `json:"id"`
	UserID          string            `json:"user_id"`
	Username        string            `json:"username"`
	AvatarURL       string            `json:"avatar_url"`
	ParentCommentID *string           `json:"parent_comment_id"`
	Body            string            `json:"body"`
	IsDeleted       bool              `json:"is_deleted"`
	IsOwn           bool              `json:"is_own"`
	Replies         []commentResponse `json:"replies"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

func newCommentResponse(comment *repository.NovelComment, viewerID string) commentResponse {
	replies := make([]commentResponse, 0, len(comment.Replies))
	for _, reply := range comment.Replies {
		replies = append(replies, newCommentResponse(reply, viewerID))
	}
	return commentResponse{
		ID:              comment.ID,
		UserID:          comment.UserID,
		Username:        comment.Username,
		AvatarURL:       comment.AvatarURL,
		ParentCommentID: comment.ParentCommentID,
		Body:            comment.Body,
		IsDeleted:       comment.IsDeleted,
		IsOwn:           viewerID != "" && viewerID == comment.UserID,
		Replies:         replies,
		CreatedAt:       comment.CreatedAt,
		UpdatedAt:       comment.UpdatedAt,
	}
}

func (handler *NovelCommentHandler) viewerID(request *http.Request) string {
	if userID, ok := middleware.OptionalUserID(handler.jwtSecret, request); ok {
		return userID
	}
	return ""
}

// List: GET /novels/{id}/comments?page=&page_size=
func (handler *NovelCommentHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	comments, total, err := handler.comments.ListForNovel(request.Context(), request.PathValue("id"), page, pageSize)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	viewerID := handler.viewerID(request)
	items := make([]commentResponse, 0, len(comments))
	for _, comment := range comments {
		items = append(items, newCommentResponse(comment, viewerID))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items, "total": total})
}

type createCommentRequest struct {
	Body            string  `json:"body"`
	ParentCommentID *string `json:"parent_comment_id"`
}

// Create: POST /novels/{id}/comments — requires the Authenticate middleware.
func (handler *NovelCommentHandler) Create(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	var body createCommentRequest
	if !decodeJSON(responseWriter, request, &body) {
		return
	}
	comment, err := handler.comments.Create(
		request.Context(), request.PathValue("id"), userID, body.ParentCommentID, body.Body)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusCreated, newCommentResponse(comment, userID))
}

type updateCommentRequest struct {
	Body string `json:"body"`
}

// Update: PUT /comments/{id} — own comment only. Requires the
// Authenticate middleware.
func (handler *NovelCommentHandler) Update(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	var body updateCommentRequest
	if !decodeJSON(responseWriter, request, &body) {
		return
	}
	comment, err := handler.comments.Update(request.Context(), request.PathValue("id"), userID, body.Body)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newCommentResponse(comment, userID))
}

// Delete: DELETE /comments/{id} — own comment only. Requires the
// Authenticate middleware.
func (handler *NovelCommentHandler) Delete(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	if err := handler.comments.Delete(request.Context(), request.PathValue("id"), userID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}

// AdminDelete: DELETE /admin/comments/{id} — moderation removal of any
// comment (abuse/spam), audit-logged the same as admin's other
// single-item moderation actions (see AdminNovelHandler.DeleteRating).
func (handler *NovelCommentHandler) AdminDelete(responseWriter http.ResponseWriter, request *http.Request) {
	commentID := request.PathValue("id")
	if err := handler.comments.AdminDelete(request.Context(), commentID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	handler.auditLogger.Log(actorID, actorName, "comment.deleted", "comment", commentID, nil)
	responseWriter.WriteHeader(http.StatusNoContent)
}
