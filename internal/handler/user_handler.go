package handler

import (
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

const maxAvatarBytes = 5 << 20 // 5 MB

// Allowed avatar formats, keyed by sniffed content type.
var avatarExtensionsByContentType = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

type UserHandler struct {
	users            *repository.UserRepository
	avatarsDirectory string
}

func NewUserHandler(users *repository.UserRepository, avatarsDirectory string) *UserHandler {
	return &UserHandler{users: users, avatarsDirectory: avatarsDirectory}
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

	request.Body = http.MaxBytesReader(responseWriter, request.Body, maxAvatarBytes)
	if err := request.ParseMultipartForm(maxAvatarBytes); err != nil {
		writeJSON(responseWriter, http.StatusBadRequest, map[string]string{
			"error": "avatar image is too large (max 5MB) or the form is invalid",
		})
		return
	}

	uploadedFile, _, err := request.FormFile("avatar")
	if err != nil {
		writeJSON(responseWriter, http.StatusBadRequest, map[string]string{
			"error": "multipart field 'avatar' is required",
		})
		return
	}
	defer uploadedFile.Close()

	// Sniff the real content type from the first bytes — the client's
	// claimed filename/type is not trusted.
	sniffBuffer := make([]byte, 512)
	bytesRead, err := io.ReadFull(uploadedFile, sniffBuffer)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		writeJSON(responseWriter, http.StatusBadRequest, map[string]string{
			"error": "could not read the uploaded file",
		})
		return
	}
	sniffBuffer = sniffBuffer[:bytesRead]

	extension, allowed := avatarExtensionsByContentType[http.DetectContentType(sniffBuffer)]
	if !allowed {
		writeJSON(responseWriter, http.StatusBadRequest, map[string]string{
			"error": "avatar must be a JPEG, PNG, or WebP image",
		})
		return
	}

	if err := os.MkdirAll(userHandler.avatarsDirectory, 0o755); err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}
	userHandler.removeStoredAvatarFiles(userID, extension)

	destinationPath := filepath.Join(userHandler.avatarsDirectory, userID+extension)
	destination, err := os.Create(destinationPath)
	if err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}
	defer destination.Close()
	if _, err := destination.Write(sniffBuffer); err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}
	if _, err := io.Copy(destination, uploadedFile); err != nil {
		userHandler.internalError(responseWriter, err)
		return
	}

	// ?v= busts client-side image caches after each upload
	avatarURL := "/uploads/avatars/" + userID + extension +
		"?v=" + strconv.FormatInt(time.Now().Unix(), 10)
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
	userHandler.removeStoredAvatarFiles(userID, "")

	userHandler.respondWithUser(responseWriter, request, userID)
}

// removeStoredAvatarFiles deletes this user's stored avatar files,
// keeping only the given extension ("" keeps none).
func (userHandler *UserHandler) removeStoredAvatarFiles(userID, keepExtension string) {
	for _, extension := range avatarExtensionsByContentType {
		if extension != keepExtension {
			os.Remove(filepath.Join(userHandler.avatarsDirectory, userID+extension))
		}
	}
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
		"created_at": user.CreatedAt,
	})
}

func (userHandler *UserHandler) internalError(responseWriter http.ResponseWriter, err error) {
	log.Printf("internal error: %v", err)
	writeJSON(responseWriter, http.StatusInternalServerError, map[string]string{
		"error": "internal server error",
	})
}
