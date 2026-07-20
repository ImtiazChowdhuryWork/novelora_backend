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

// NotificationHandler serves the signed-in reader's in-app inbox
// (Authenticate) and the admin composer's broadcast send (RequireAdmin
// too — wired separately in main.go).
type NotificationHandler struct {
	notifications *repository.NotificationRepository
	broadcasts    *service.BroadcastService
	auditLogger   *audit.Logger
}

func NewNotificationHandler(
	notifications *repository.NotificationRepository,
	broadcasts *service.BroadcastService,
	auditLogger *audit.Logger,
) *NotificationHandler {
	return &NotificationHandler{
		notifications: notifications,
		broadcasts:    broadcasts,
		auditLogger:   auditLogger,
	}
}

type broadcastRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// Broadcast: POST /admin/notifications/broadcast — sends a general
// announcement to every account's inbox + device.
func (notificationHandler *NotificationHandler) Broadcast(responseWriter http.ResponseWriter, request *http.Request) {
	var broadcastRequest broadcastRequest
	if !decodeJSON(responseWriter, request, &broadcastRequest) {
		return
	}
	if err := notificationHandler.broadcasts.Send(request.Context(), broadcastRequest.Title, broadcastRequest.Body); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	notificationHandler.auditLogger.Log(actorID, actorName, "notification.broadcast", "notification", "",
		map[string]any{"title": broadcastRequest.Title})
	responseWriter.WriteHeader(http.StatusNoContent)
}

type notificationResponse struct {
	ID        string    `json:"id"`
	NovelID   *string   `json:"novel_id"`
	ChapterID *string   `json:"chapter_id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	IsRead    bool      `json:"is_read"`
	CreatedAt time.Time `json:"created_at"`
}

func newNotificationResponse(notification *repository.Notification) notificationResponse {
	return notificationResponse{
		ID:        notification.ID,
		NovelID:   notification.NovelID,
		ChapterID: notification.ChapterID,
		Title:     notification.Title,
		Body:      notification.Body,
		IsRead:    notification.IsRead,
		CreatedAt: notification.CreatedAt,
	}
}

// List: GET /users/me/notifications?page=&page_size=
func (notificationHandler *NotificationHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))

	notifications, total, err := notificationHandler.notifications.ListForUser(request.Context(), userID, page, pageSize)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	unreadCount, err := notificationHandler.notifications.UnreadCount(request.Context(), userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	items := make([]notificationResponse, 0, len(notifications))
	for _, notification := range notifications {
		items = append(items, newNotificationResponse(notification))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"items":        items,
		"total":        total,
		"unread_count": unreadCount,
	})
}

// UnreadCount: GET /users/me/notifications/unread-count
func (notificationHandler *NotificationHandler) UnreadCount(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	unreadCount, err := notificationHandler.notifications.UnreadCount(request.Context(), userID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"unread_count": unreadCount})
}

// MarkRead: PUT /users/me/notifications/{id}/read
func (notificationHandler *NotificationHandler) MarkRead(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	err := notificationHandler.notifications.MarkRead(request.Context(), userID, request.PathValue("id"))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}

// MarkAllRead: PUT /users/me/notifications/read-all
func (notificationHandler *NotificationHandler) MarkAllRead(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	if err := notificationHandler.notifications.MarkAllRead(request.Context(), userID); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}
