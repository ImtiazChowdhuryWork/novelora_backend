package handler

import (
	"net/http"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

// AuthorModerationHandler backs the admin report detail view's author
// panel — all admin-only, all keyed by the {authorName} path segment
// (URL-decoded automatically by net/http's router) plus an optional
// ?owner_user_id= query param the frontend supplies whenever the
// report's novel has a real account (migration 0030) — see
// ownerUserIDFromQuery. Absent/empty keeps today's name-only behavior.
type AuthorModerationHandler struct {
	moderation  *service.AuthorModerationService
	auditLogger *audit.Logger
}

func NewAuthorModerationHandler(moderation *service.AuthorModerationService, auditLogger *audit.Logger) *AuthorModerationHandler {
	return &AuthorModerationHandler{moderation: moderation, auditLogger: auditLogger}
}

// ownerUserIDFromQuery reads the optional ?owner_user_id= param, nil
// when absent/empty so callers fall back to the name-keyed path.
func ownerUserIDFromQuery(request *http.Request) *string {
	if value := request.URL.Query().Get("owner_user_id"); value != "" {
		return &value
	}
	return nil
}

// OtherNovels: GET /admin/authors/{authorName}/novels?exclude=<novelId>&owner_user_id=<id>
func (handler *AuthorModerationHandler) OtherNovels(responseWriter http.ResponseWriter, request *http.Request) {
	authorName := request.PathValue("authorName")
	excludeNovelID := request.URL.Query().Get("exclude")

	novels, commentCounts, err := handler.moderation.OtherNovels(request.Context(), authorName, ownerUserIDFromQuery(request), excludeNovelID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]novelResponse, 0, len(novels))
	for _, novel := range novels {
		item := newNovelResponse(novel)
		item.CommentCount = commentCounts[novel.ID]
		items = append(items, item)
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

// Reports: GET /admin/authors/{authorName}/reports?owner_user_id=<id> —
// the report detail drawer's report-history section.
func (handler *AuthorModerationHandler) Reports(responseWriter http.ResponseWriter, request *http.Request) {
	reports, err := handler.moderation.Reports(request.Context(), request.PathValue("authorName"), ownerUserIDFromQuery(request))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]reportResponse, 0, len(reports))
	for _, report := range reports {
		items = append(items, newReportResponse(report))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

type authorProfileSummaryResponse struct {
	HasAccount            bool       `json:"has_account"`
	IsBanned              bool       `json:"is_banned"`
	JoinedAt              *time.Time `json:"joined_at"`
	PenName               string     `json:"pen_name"`
	Bio                   string     `json:"bio"`
	NovelCount            int        `json:"novel_count"`
	PublishedChapterCount int        `json:"published_chapter_count"`
	TotalViews            int64      `json:"total_views"`
}

// Profile: GET /admin/authors/{authorName}/profile?owner_user_id=<id> —
// the report detail drawer's Profile tab: account status + a catalog
// rollup, alongside the strike history Strikes already serves.
func (handler *AuthorModerationHandler) Profile(responseWriter http.ResponseWriter, request *http.Request) {
	summary, err := handler.moderation.Profile(request.Context(), request.PathValue("authorName"), ownerUserIDFromQuery(request))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	response := authorProfileSummaryResponse{
		HasAccount:            summary.HasAccount,
		IsBanned:              summary.IsBanned,
		PenName:               summary.PenName,
		Bio:                   summary.Bio,
		NovelCount:            summary.NovelCount,
		PublishedChapterCount: summary.PublishedChapterCount,
		TotalViews:            summary.TotalViews,
	}
	if summary.HasAccount {
		response.JoinedAt = &summary.JoinedAt
	}
	writeJSON(responseWriter, http.StatusOK, response)
}

type strikeResponse struct {
	ID            string    `json:"id"`
	AuthorName    string    `json:"author_name"`
	OwnerUserID   *string   `json:"owner_user_id"`
	Note          string    `json:"note"`
	Severity      string    `json:"severity"`
	CreatedByName string    `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
}

func newStrikeResponse(strike *repository.AuthorStrike) strikeResponse {
	return strikeResponse{
		ID:            strike.ID,
		AuthorName:    strike.AuthorName,
		OwnerUserID:   strike.OwnerUserID,
		Note:          strike.Note,
		Severity:      strike.Severity,
		CreatedByName: strike.CreatedByName,
		CreatedAt:     strike.CreatedAt,
	}
}

// Strikes: GET /admin/authors/{authorName}/strikes?owner_user_id=<id>
func (handler *AuthorModerationHandler) Strikes(responseWriter http.ResponseWriter, request *http.Request) {
	strikes, err := handler.moderation.Strikes(request.Context(), request.PathValue("authorName"), ownerUserIDFromQuery(request))
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	items := make([]strikeResponse, 0, len(strikes))
	for _, strike := range strikes {
		items = append(items, newStrikeResponse(strike))
	}
	writeJSON(responseWriter, http.StatusOK, map[string]any{"items": items})
}

type addStrikeRequest struct {
	Note     string `json:"note"`
	Severity string `json:"severity"`
}

// AddStrike: POST /admin/authors/{authorName}/strikes?owner_user_id=<id> — {note, severity}.
func (handler *AuthorModerationHandler) AddStrike(responseWriter http.ResponseWriter, request *http.Request) {
	authorName := request.PathValue("authorName")

	var body addStrikeRequest
	if !decodeJSON(responseWriter, request, &body) {
		return
	}
	actorID, actorName := actorFromContext(request.Context())
	strike, err := handler.moderation.AddStrike(request.Context(), authorName, ownerUserIDFromQuery(request), body.Note, body.Severity, actorID)
	if err != nil {
		writeServiceError(responseWriter, err)
		return
	}
	handler.auditLogger.Log(actorID, actorName, "author.strike", "author", authorName,
		map[string]any{"note": body.Note, "severity": body.Severity})
	writeJSON(responseWriter, http.StatusCreated, newStrikeResponse(strike))
}
