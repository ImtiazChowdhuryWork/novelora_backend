package handler

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

type AuthHandler struct {
	authService *service.AuthService
}

func NewAuthHandler(authService *service.AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

type registerRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

type googleLoginRequest struct {
	IDToken string `json:"id_token"`
}

type userResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	AvatarURL string    `json:"avatar_url"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type authResponse struct {
	User         userResponse `json:"user"`
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"refresh_token"`
	ExpiresIn    int64        `json:"expires_in"`
}

func newAuthResponse(result *service.AuthResult) authResponse {
	return authResponse{
		User: userResponse{
			ID:        result.User.ID,
			Username:  result.User.Username,
			Email:     result.User.Email,
			AvatarURL: result.User.AvatarURL,
			Role:      result.User.Role,
			CreatedAt: result.User.CreatedAt,
		},
		AccessToken:  result.AccessToken,
		RefreshToken: result.RefreshToken,
		ExpiresIn:    result.ExpiresIn,
	}
}

// Register creates a new user account and signs it in.
func (authHandler *AuthHandler) Register(responseWriter http.ResponseWriter, request *http.Request) {
	var requestBody registerRequest
	if !decodeJSON(responseWriter, request, &requestBody) {
		return
	}

	result, err := authHandler.authService.Register(
		request.Context(), requestBody.Username, requestBody.Email, requestBody.Password)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusCreated, newAuthResponse(result))
}

// Login authenticates a user and issues tokens.
func (authHandler *AuthHandler) Login(responseWriter http.ResponseWriter, request *http.Request) {
	var requestBody loginRequest
	if !decodeJSON(responseWriter, request, &requestBody) {
		return
	}

	result, err := authHandler.authService.Login(
		request.Context(), requestBody.Email, requestBody.Password)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newAuthResponse(result))
}

// GoogleLogin signs in with a Google ID token obtained on the device.
func (authHandler *AuthHandler) GoogleLogin(responseWriter http.ResponseWriter, request *http.Request) {
	var requestBody googleLoginRequest
	if !decodeJSON(responseWriter, request, &requestBody) {
		return
	}
	if requestBody.IDToken == "" {
		writeError(responseWriter, http.StatusBadRequest, "id_token is required")
		return
	}

	result, err := authHandler.authService.LoginWithGoogle(request.Context(), requestBody.IDToken)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newAuthResponse(result))
}

// RefreshToken exchanges a refresh token for a new token pair.
func (authHandler *AuthHandler) RefreshToken(responseWriter http.ResponseWriter, request *http.Request) {
	var requestBody refreshRequest
	if !decodeJSON(responseWriter, request, &requestBody) {
		return
	}

	result, err := authHandler.authService.Refresh(request.Context(), requestBody.RefreshToken)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	writeJSON(responseWriter, http.StatusOK, newAuthResponse(result))
}

// Logout revokes the presented refresh token.
func (authHandler *AuthHandler) Logout(responseWriter http.ResponseWriter, request *http.Request) {
	var requestBody refreshRequest
	if !decodeJSON(responseWriter, request, &requestBody) {
		return
	}

	if err := authHandler.authService.Logout(request.Context(), requestBody.RefreshToken); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}

// decodeJSON parses the request body into destination; on failure it
// writes a 400 and returns false.
func decodeJSON(responseWriter http.ResponseWriter, request *http.Request, destination any) bool {
	request.Body = http.MaxBytesReader(responseWriter, request.Body, 1<<20)
	if err := json.NewDecoder(request.Body).Decode(destination); err != nil {
		writeError(responseWriter, http.StatusBadRequest, "request body is not valid JSON")
		return false
	}
	return true
}

func writeError(responseWriter http.ResponseWriter, statusCode int, message string) {
	writeJSON(responseWriter, statusCode, map[string]string{"error": message})
}

// writeServiceError maps service/repository errors to HTTP statuses.
func writeServiceError(responseWriter http.ResponseWriter, err error) {
	var validationError *service.ValidationError
	switch {
	case errors.As(err, &validationError):
		writeError(responseWriter, http.StatusBadRequest, validationError.Message)
	case errors.Is(err, repository.ErrEmailTaken),
		errors.Is(err, repository.ErrUsernameTaken):
		writeError(responseWriter, http.StatusConflict, err.Error())
	case errors.Is(err, repository.ErrNovelNotFound):
		writeError(responseWriter, http.StatusNotFound, err.Error())
	case errors.Is(err, service.ErrInvalidCredentials),
		errors.Is(err, service.ErrInvalidRefreshToken),
		errors.Is(err, service.ErrInvalidGoogleToken):
		writeError(responseWriter, http.StatusUnauthorized, err.Error())
	case errors.Is(err, service.ErrGoogleLoginNotConfigured):
		writeError(responseWriter, http.StatusServiceUnavailable, err.Error())
	default:
		log.Printf("internal error: %v", err)
		writeError(responseWriter, http.StatusInternalServerError, "internal server error")
	}
}
