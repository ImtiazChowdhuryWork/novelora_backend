package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

// AdminUserHandler is the dashboard's Users page: search/list accounts,
// change role, ban/unban. There's no user-generated content
// (comments/reviews) yet, so banning is the only moderation action —
// this is the natural place to add a content queue once one exists.
type AdminUserHandler struct {
	users *repository.UserRepository
}

func NewAdminUserHandler(users *repository.UserRepository) *AdminUserHandler {
	return &AdminUserHandler{users: users}
}

type adminUserResponse struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Email     string    `json:"email"`
	AvatarURL string    `json:"avatar_url"`
	Role      string    `json:"role"`
	IsBanned  bool      `json:"is_banned"`
	CreatedAt time.Time `json:"created_at"`
}

func newAdminUserResponse(user *repository.User) adminUserResponse {
	return adminUserResponse{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		AvatarURL: user.AvatarURL,
		Role:      user.Role,
		IsBanned:  user.IsBanned,
		CreatedAt: user.CreatedAt,
	}
}

// List: GET /admin/users?page=&page_size=&search=
func (adminUserHandler *AdminUserHandler) List(responseWriter http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	page, _ := strconv.Atoi(query.Get("page"))
	pageSize, _ := strconv.Atoi(query.Get("page_size"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	users, total, err := adminUserHandler.users.List(request.Context(), repository.UserListFilter{
		Search:   query.Get("search"),
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}

	items := make([]adminUserResponse, 0, len(users))
	for _, user := range users {
		items = append(items, newAdminUserResponse(user))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{
		"items": items,
		"total": total,
	})
}

type roleUpdateRequest struct {
	Role string `json:"role"`
}

// UpdateRole: PUT /admin/users/{id}/role
func (adminUserHandler *AdminUserHandler) UpdateRole(responseWriter http.ResponseWriter, request *http.Request) {
	targetUserID := request.PathValue("id")
	if adminUserHandler.isSelf(request, targetUserID) {
		writeError(responseWriter, http.StatusBadRequest, "cannot change your own role")
		return
	}

	var updateRequest roleUpdateRequest
	if !decodeJSON(responseWriter, request, &updateRequest) {
		return
	}
	if updateRequest.Role != "reader" && updateRequest.Role != "admin" {
		writeError(responseWriter, http.StatusBadRequest, "role must be 'reader' or 'admin'")
		return
	}

	if err := adminUserHandler.users.UpdateRole(request.Context(), targetUserID, updateRequest.Role); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}

type banUpdateRequest struct {
	Banned bool `json:"banned"`
}

// UpdateBanned: PUT /admin/users/{id}/ban
func (adminUserHandler *AdminUserHandler) UpdateBanned(responseWriter http.ResponseWriter, request *http.Request) {
	targetUserID := request.PathValue("id")
	if adminUserHandler.isSelf(request, targetUserID) {
		writeError(responseWriter, http.StatusBadRequest, "cannot ban your own account")
		return
	}

	var updateRequest banUpdateRequest
	if !decodeJSON(responseWriter, request, &updateRequest) {
		return
	}

	if err := adminUserHandler.users.SetBanned(request.Context(), targetUserID, updateRequest.Banned); err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	responseWriter.WriteHeader(http.StatusNoContent)
}

func (adminUserHandler *AdminUserHandler) isSelf(request *http.Request, targetUserID string) bool {
	callerID, _ := request.Context().Value(middleware.UserIDContextKey).(string)
	return callerID != "" && callerID == targetUserID
}
