package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

type UserHandler struct {
	users            *repository.UserRepository
	deviceTokens     *repository.DeviceTokenRepository
	readingHistory   *service.ReadingHistoryService
	authorProfiles   *repository.AuthorProfileRepository
	jwtSecret        []byte
	avatarsDirectory string
	avatarsUrlPrefix string
}

func NewUserHandler(
	users *repository.UserRepository,
	deviceTokens *repository.DeviceTokenRepository,
	readingHistory *service.ReadingHistoryService,
	authorProfiles *repository.AuthorProfileRepository,
	jwtSecret []byte,
	avatarsDirectory string,
) *UserHandler {
	return &UserHandler{
		users:            users,
		deviceTokens:     deviceTokens,
		readingHistory:   readingHistory,
		authorProfiles:   authorProfiles,
		jwtSecret:        jwtSecret,
		avatarsDirectory: avatarsDirectory,
		avatarsUrlPrefix: "/uploads/avatars/",
	}
}

type deviceTokenRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

// RegisterDeviceToken upserts an FCM device token so chapter publishes
// can push to it. Deliberately public — notifications currently
// broadcast to every device, so requiring login would only lose
// visitors' tokens for no benefit. If a valid Bearer token happens to
// be present, the device is attributed to that account for later use.
func (userHandler *UserHandler) RegisterDeviceToken(responseWriter http.ResponseWriter, request *http.Request) {
	var tokenRequest deviceTokenRequest
	if !decodeJSON(responseWriter, request, &tokenRequest) {
		return
	}
	if tokenRequest.Token == "" {
		writeError(responseWriter, http.StatusBadRequest, "token is required")
		return
	}
	platform := tokenRequest.Platform
	if platform != "android" && platform != "ios" {
		platform = "android"
	}

	var userID *string
	if id, ok := middleware.OptionalUserID(userHandler.jwtSecret, request); ok {
		userID = &id
	}
	if err := userHandler.deviceTokens.Upsert(request.Context(), userID, tokenRequest.Token, platform); err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}

// ReadingHistory: GET /users/me/reading-history — Phase 5a's backing
// data for Library's Viewed tab. Requires the Authenticate middleware.
func (userHandler *UserHandler) ReadingHistory(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	novels, err := userHandler.readingHistory.ListForUser(request.Context(), userID)
	if err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}
	items := make([]novelResponse, 0, len(novels))
	for _, novel := range novels {
		items = append(items, newNovelResponse(novel))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

// CurrentUser returns the account of the authenticated caller.
// Requires the Authenticate middleware.
func (userHandler *UserHandler) CurrentUser(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	userHandler.respondWithUser(responseWriter, request, userID)
}

// UpdateAvatar stores an uploaded image (multipart field "avatar") and
// points the user's avatar_url at it. Requires the Authenticate middleware.
func (userHandler *UserHandler) UpdateAvatar(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	avatarURL, err := saveUploadedImage(
		request, "avatar", userHandler.avatarsDirectory,
		userHandler.avatarsUrlPrefix, userID)
	if err != nil {
		writeImageUploadError(responseWriter, err)
		return
	}

	if err := userHandler.users.UpdateAvatarURL(request.Context(), userID, avatarURL); err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}
	userHandler.respondWithUser(responseWriter, request, userID)
}

// RemoveAvatar clears the user's avatar and deletes the stored file.
// Requires the Authenticate middleware.
func (userHandler *UserHandler) RemoveAvatar(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

	if err := userHandler.users.UpdateAvatarURL(request.Context(), userID, ""); err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}
	removeStaleImageFiles(userHandler.avatarsDirectory, userID, "")

	userHandler.respondWithUser(responseWriter, request, userID)
}

func (userHandler *UserHandler) respondWithUser(responseWriter http.ResponseWriter, request *http.Request, userID string) {
	user, err := userHandler.users.FindByID(request.Context(), userID)
	if errors.Is(err, repository.ErrUserNotFound) {
		writeJSON(responseWriter, http.StatusNotFound, map[string]string{
			"error": "user no longer exists",
		})
		return
	}
	if err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}

	penName := ""
	authorProfile, err := userHandler.authorProfiles.GetByUserID(request.Context(), userID)
	if err == nil {
		penName = authorProfile.PenName
	} else if !errors.Is(err, repository.ErrAuthorProfileNotFound) {
		userHandler.internalError(responseWriter, err)
		return
	}

	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"id":         user.ID,
		"username":   user.Username,
		"email":      user.Email,
		"avatar_url": user.AvatarURL,
		"role":       user.Role,
		"is_author":  penName != "",
		"pen_name":   penName,
		"created_at": user.CreatedAt,
	})
}

func (userHandler *UserHandler) internalError(responseWriter http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeJSON(responseWriter, http.StatusInternalServerError, map[string]string{
		"error": "internal server error",
	})
}
