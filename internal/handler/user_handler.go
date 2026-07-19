package handler

import (
	"errors"
	"log"
	"net/http"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

type UserHandler struct {
	users            *repository.UserRepository
	deviceTokens     *repository.DeviceTokenRepository
	avatarsDirectory string
	avatarsUrlPrefix string
}

func NewUserHandler(
	users *repository.UserRepository,
	deviceTokens *repository.DeviceTokenRepository,
	avatarsDirectory string,
) *UserHandler {
	return &UserHandler{
		users:            users,
		deviceTokens:     deviceTokens,
		avatarsDirectory: avatarsDirectory,
		avatarsUrlPrefix: "/uploads/avatars/",
	}
}

type deviceTokenRequest struct {
	Token    string `json:"token"`
	Platform string `json:"platform"`
}

// RegisterDeviceToken upserts the caller's FCM device token so chapter
// publishes can push to it. Requires the Authenticate middleware.
func (userHandler *UserHandler) RegisterDeviceToken(responseWriter http.ResponseWriter, request *http.Request) {
	userID, _ := request.Context().Value(middleware.UserIDContextKey).(string)

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

	if err := userHandler.deviceTokens.Upsert(request.Context(), userID, tokenRequest.Token, platform); err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
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

	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"id":         user.ID,
		"username":   user.Username,
		"email":      user.Email,
		"avatar_url": user.AvatarURL,
		"role":       user.Role,
		"created_at": user.CreatedAt,
	})
}

func (userHandler *UserHandler) internalError(responseWriter http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeJSON(responseWriter, http.StatusInternalServerError, map[string]string{
		"error": "internal server error",
	})
}
