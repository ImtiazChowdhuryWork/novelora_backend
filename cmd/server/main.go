package main

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/config"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/database"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/handler"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/push"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

func main() {
	configuration, err := config.Load()
	if err != nil {
		log.Fatalf("load configuration: %v", err)
	}

	startupContext, cancelStartup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelStartup()

	pool, err := database.Connect(startupContext, configuration.DatabaseURL)
	if err != nil {
		log.Fatalf("connect to database: %v", err)
	}
	defer pool.Close()

	if err := database.Migrate(startupContext, pool); err != nil {
		log.Fatalf("run migrations: %v", err)
	}

	userRepository := repository.NewUserRepository(pool)
	refreshTokenRepository := repository.NewRefreshTokenRepository(pool)
	authorProfileRepository := repository.NewAuthorProfileRepository(pool)

	eventHub := realtime.NewHub()
	go eventHub.Run()

	authService := service.NewAuthService(
		userRepository,
		refreshTokenRepository,
		authorProfileRepository,
		eventHub,
		configuration.JWTSecret,
		configuration.AccessTokenTTL,
		configuration.RefreshTokenTTL,
		configuration.GoogleClientID,
	)
	novelRepository := repository.NewNovelRepository(pool)
	genreRepository := repository.NewGenreRepository(pool)
	chapterRepository := repository.NewChapterRepository(pool)
	deviceTokenRepository := repository.NewDeviceTokenRepository(pool)
	notificationRepository := repository.NewNotificationRepository(pool)
	sectionMembershipRepository := repository.NewSectionMembershipRepository(pool)
	discoverSectionRepository := repository.NewDiscoverSectionRepository(pool)
	novelSectionOverrideRepository := repository.NewNovelSectionOverrideRepository(pool)
	readingHistoryRepository := repository.NewReadingHistoryRepository(pool)
	novelRatingRepository := repository.NewNovelRatingRepository(pool)
	novelCommentRepository := repository.NewNovelCommentRepository(pool)
	novelSupportRepository := repository.NewNovelSupportRepository(pool)
	novelReportRepository := repository.NewNovelReportRepository(pool)
	moderationActionRepository := repository.NewModerationActionRepository(pool)
	releaseRequestRepository := repository.NewReleaseRequestRepository(pool)
	authorStrikeRepository := repository.NewAuthorStrikeRepository(pool)
	auditLogRepository := repository.NewAuditLogRepository(pool)
	auditLogger := audit.NewLogger(auditLogRepository)
	outboxRepository := repository.NewOutboxRepository(pool)

	var chapterNotifier push.Notifier = push.NoopNotifier{}
	if configuration.FirebaseCredentialsPath != "" {
		fcmNotifier, err := push.NewFCMNotifier(startupContext, configuration.FirebaseCredentialsPath)
		if err != nil {
			log.Printf("push: failed to initialize FCM, notifications disabled: %v", err)
		} else {
			chapterNotifier = fcmNotifier
			log.Println("push: FCM notifications enabled")
		}
	} else {
		log.Println("push: FIREBASE_CREDENTIALS_JSON not set, notifications disabled")
	}

	novelService := service.NewNovelService(
		novelRepository, genreRepository, novelRatingRepository, novelSupportRepository,
		readingHistoryRepository, deviceTokenRepository, notificationRepository, chapterNotifier, eventHub,
		outboxRepository)
	chapterService := service.NewChapterService(
		chapterRepository, novelRepository, deviceTokenRepository, notificationRepository, chapterNotifier, eventHub,
		outboxRepository)
	go runScheduledPublishTicker(chapterService)

	outboxProcessor := service.NewOutboxProcessor(outboxRepository, chapterService, novelService)
	go runOutboxProcessorTicker(outboxProcessor)

	discoverSectionService := service.NewDiscoverSectionService(
		discoverSectionRepository, novelSectionOverrideRepository, novelRepository, genreRepository,
		readingHistoryRepository, eventHub)
	readingHistoryService := service.NewReadingHistoryService(readingHistoryRepository, novelRepository, genreRepository)
	novelCommentService := service.NewNovelCommentService(novelCommentRepository, novelRepository, eventHub)
	novelReportService := service.NewNovelReportService(
		novelReportRepository, novelRepository, chapterRepository,
		moderationActionRepository, releaseRequestRepository, novelService, chapterService,
		notificationRepository, deviceTokenRepository, chapterNotifier, eventHub)
	authorModerationService := service.NewAuthorModerationService(novelRepository, authorStrikeRepository, novelReportRepository, eventHub)
	go runReportPurgeTicker(novelReportService)

	rankingNotificationService := service.NewRankingNotificationService(
		discoverSectionService, sectionMembershipRepository,
		notificationRepository, deviceTokenRepository, chapterNotifier, eventHub)
	go runRankingDetectionTicker(rankingNotificationService)

	authHandler := handler.NewAuthHandler(authService)
	userHandler := handler.NewUserHandler(
		userRepository, deviceTokenRepository, readingHistoryService, authorProfileRepository, configuration.JWTSecret,
		filepath.Join(configuration.UploadsDirectory, "avatars"))
	adminNovelHandler := handler.NewAdminNovelHandler(novelService, auditLogger, configuration.UploadsDirectory)
	adminChapterHandler := handler.NewAdminChapterHandler(chapterService, auditLogger)
	authorNovelHandler := handler.NewAuthorNovelHandler(novelService, auditLogger, configuration.UploadsDirectory)
	authorChapterHandler := handler.NewAuthorChapterHandler(chapterService, novelService, novelReportRepository, auditLogger)
	publicNovelHandler := handler.NewPublicNovelHandler(novelService, chapterService, readingHistoryService, novelReportService, configuration.JWTSecret)
	novelCommentHandler := handler.NewNovelCommentHandler(novelCommentService, auditLogger, configuration.JWTSecret)
	novelReportHandler := handler.NewNovelReportHandler(novelReportService, auditLogger, configuration.UploadsDirectory)
	authorModerationHandler := handler.NewAuthorModerationHandler(authorModerationService, auditLogger)
	broadcastService := service.NewBroadcastService(deviceTokenRepository, notificationRepository, chapterNotifier, eventHub)
	notificationHandler := handler.NewNotificationHandler(notificationRepository, broadcastService, auditLogger)
	genreHandler := handler.NewGenreHandler(genreRepository, auditLogger)
	discoverSectionHandler := handler.NewDiscoverSectionHandler(discoverSectionService, auditLogger, configuration.JWTSecret)
	adminUserHandler := handler.NewAdminUserHandler(userRepository, auditLogger)
	adminStatsHandler := handler.NewAdminStatsHandler(repository.NewStatsRepository(pool))
	adminAuditLogHandler := handler.NewAdminAuditLogHandler(auditLogRepository)

	mux := http.NewServeMux()

	// Rate limiting: a loose global limit wraps every route (set on the
	// server below), plus a much stricter one on the credential-facing
	// auth routes to blunt brute-force/credential-stuffing attempts.
	globalRateLimiter := middleware.NewIPRateLimiter(300, 60)
	authRateLimiter := middleware.NewIPRateLimiter(10, 5)

	// Health check
	mux.HandleFunc("GET /health", handler.Health)

	// Auth (matches the Flutter app's ApiEndpoints)
	mux.Handle("POST /api/v1/auth/register", authRateLimiter.Middleware(http.HandlerFunc(authHandler.Register)))
	mux.Handle("POST /api/v1/auth/login", authRateLimiter.Middleware(http.HandlerFunc(authHandler.Login)))
	mux.Handle("POST /api/v1/auth/google", authRateLimiter.Middleware(http.HandlerFunc(authHandler.GoogleLogin)))
	mux.Handle("POST /api/v1/auth/refresh", authRateLimiter.Middleware(http.HandlerFunc(authHandler.RefreshToken)))
	mux.HandleFunc("POST /api/v1/auth/logout", authHandler.Logout)

	// Users (require a valid access token)
	mux.Handle("GET /api/v1/users/me", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(userHandler.CurrentUser)))
	mux.Handle("POST /api/v1/users/me/author-profile", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(authHandler.BecomeAuthor)))
	mux.Handle("PUT /api/v1/users/me/author-profile", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(userHandler.UpdateAuthorProfile)))
	mux.Handle("PUT /api/v1/users/me/avatar", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(userHandler.UpdateAvatar)))
	mux.Handle("DELETE /api/v1/users/me/avatar", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(userHandler.RemoveAvatar)))
	// Deliberately public: see RegisterDeviceToken's doc comment
	mux.HandleFunc("PUT /api/v1/users/me/device-token", userHandler.RegisterDeviceToken)

	// In-app notification inbox
	mux.Handle("GET /api/v1/users/me/notifications", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(notificationHandler.List)))
	mux.Handle("GET /api/v1/users/me/notifications/unread-count", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(notificationHandler.UnreadCount)))
	mux.Handle("PUT /api/v1/users/me/notifications/read-all", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(notificationHandler.MarkAllRead)))
	mux.Handle("PUT /api/v1/users/me/notifications/{id}/read", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(notificationHandler.MarkRead)))
	mux.Handle("GET /api/v1/users/me/reading-history", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(userHandler.ReadingHistory)))
	mux.Handle("GET /api/v1/users/me/reports", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(novelReportHandler.Mine)))

	// Uploaded files (avatars)
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/",
		http.FileServer(http.Dir(configuration.UploadsDirectory))))

	// Public catalog (reader-facing; published content only)
	mux.HandleFunc("GET /api/v1/novels", publicNovelHandler.List)
	mux.HandleFunc("GET /api/v1/novels/{id}", publicNovelHandler.Get)
	mux.HandleFunc("GET /api/v1/novels/{id}/chapters", publicNovelHandler.Chapters)
	mux.HandleFunc("GET /api/v1/chapters/{id}", publicNovelHandler.Chapter)
	mux.HandleFunc("GET /api/v1/genres", genreHandler.List)
	mux.HandleFunc("GET /api/v1/discover-sections", discoverSectionHandler.ListActive)
	mux.HandleFunc("GET /api/v1/discover-sections/{key}/novels", discoverSectionHandler.Novels)

	// Reader ratings (Phase 5b) — own rating only; requires login
	mux.Handle("PUT /api/v1/novels/{id}/rating", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(publicNovelHandler.Rate)))
	mux.Handle("DELETE /api/v1/novels/{id}/rating", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(publicNovelHandler.RemoveRating)))

	// Support (Phase 5d) — a free tap, own support only; requires login
	mux.Handle("PUT /api/v1/novels/{id}/support", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(publicNovelHandler.Support)))
	mux.Handle("DELETE /api/v1/novels/{id}/support", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(publicNovelHandler.Unsupport)))

	// Comments (Phase 5c) — reading is public; posting/editing/removing
	// your own requires login.
	mux.HandleFunc("GET /api/v1/novels/{id}/comments", novelCommentHandler.List)
	mux.Handle("POST /api/v1/novels/{id}/comments", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(novelCommentHandler.Create)))
	mux.Handle("PUT /api/v1/comments/{id}", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(novelCommentHandler.Update)))
	mux.Handle("DELETE /api/v1/comments/{id}", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(novelCommentHandler.Delete)))

	// Reports (Book Detail's flag icon) — requires login, matching
	// ratings/comments/support.
	mux.Handle("POST /api/v1/novels/{id}/report", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(novelReportHandler.Create)))
	mux.Handle("DELETE /api/v1/reports/{id}", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(novelReportHandler.Delete)))

	// Admin: novels (JWT + admin role)
	requireAdmin := func(handlerFunc http.HandlerFunc) http.Handler {
		return middleware.Authenticate(configuration.JWTSecret,
			middleware.RequireAdmin(handlerFunc))
	}
	mux.Handle("GET /api/v1/admin/novels", requireAdmin(adminNovelHandler.List))
	mux.Handle("POST /api/v1/admin/novels", requireAdmin(adminNovelHandler.Create))
	mux.Handle("PUT /api/v1/admin/novels/reorder", requireAdmin(adminNovelHandler.Reorder))
	mux.Handle("GET /api/v1/admin/novels/{id}", requireAdmin(adminNovelHandler.Get))
	mux.Handle("PUT /api/v1/admin/novels/{id}", requireAdmin(adminNovelHandler.Update))
	mux.Handle("DELETE /api/v1/admin/novels/{id}", requireAdmin(adminNovelHandler.Delete))
	mux.Handle("POST /api/v1/admin/novels/{id}/restore", requireAdmin(adminNovelHandler.Restore))
	mux.Handle("PUT /api/v1/admin/novels/{id}/cover", requireAdmin(adminNovelHandler.UpdateCover))
	mux.Handle("GET /api/v1/admin/novels/{id}/ratings", requireAdmin(adminNovelHandler.Ratings))
	mux.Handle("DELETE /api/v1/admin/novels/{id}/ratings/{userId}", requireAdmin(adminNovelHandler.DeleteRating))

	// Author: own novels/chapters (JWT + author capability — see
	// middleware.RequireAuthor's doc comment on why this checks a
	// capability flag, not the mutually-exclusive role value)
	requireAuthor := func(handlerFunc http.HandlerFunc) http.Handler {
		return middleware.Authenticate(configuration.JWTSecret,
			middleware.RequireAuthor(handlerFunc))
	}
	mux.Handle("GET /api/v1/author/novels", requireAuthor(authorNovelHandler.List))
	mux.Handle("POST /api/v1/author/novels", requireAuthor(authorNovelHandler.Create))
	mux.Handle("GET /api/v1/author/novels/{id}", requireAuthor(authorNovelHandler.Get))
	mux.Handle("PUT /api/v1/author/novels/{id}", requireAuthor(authorNovelHandler.Update))
	mux.Handle("PUT /api/v1/author/novels/{id}/cover", requireAuthor(authorNovelHandler.UpdateCover))
	mux.Handle("DELETE /api/v1/author/novels/{id}", requireAuthor(authorNovelHandler.Delete))
	mux.Handle("GET /api/v1/author/novels/{id}/chapters", requireAuthor(authorChapterHandler.ListByNovel))
	mux.Handle("POST /api/v1/author/novels/{id}/chapters", requireAuthor(authorChapterHandler.Create))
	mux.Handle("POST /api/v1/author/novels/{id}/chapters/import", requireAuthor(authorChapterHandler.Import))
	mux.Handle("GET /api/v1/author/chapters/{id}", requireAuthor(authorChapterHandler.Get))
	mux.Handle("PUT /api/v1/author/chapters/{id}", requireAuthor(authorChapterHandler.Update))
	mux.Handle("PUT /api/v1/author/chapters/{id}/status", requireAuthor(authorChapterHandler.UpdateStatus))
	mux.Handle("PUT /api/v1/author/chapters/{id}/schedule", requireAuthor(authorChapterHandler.Schedule))
	mux.Handle("DELETE /api/v1/author/chapters/{id}", requireAuthor(authorChapterHandler.Delete))
	mux.Handle("GET /api/v1/author/reports", requireAuthor(novelReportHandler.ListForOwner))
	mux.Handle("GET /api/v1/author/reports/{id}/release-requests", requireAuthor(novelReportHandler.ReleaseRequestsForOwner))
	mux.Handle("GET /api/v1/author/reports/{id}/moderation-actions", requireAuthor(novelReportHandler.ModerationActionsForOwner))
	mux.Handle("POST /api/v1/author/reports/{id}/release-requests", requireAuthor(novelReportHandler.SubmitReleaseRequest))

	// Admin: comment moderation
	mux.Handle("GET /api/v1/admin/novels/{id}/comments", requireAdmin(novelCommentHandler.List))
	mux.Handle("DELETE /api/v1/admin/comments/{id}", requireAdmin(novelCommentHandler.AdminDelete))

	// Admin: report moderation — moderation workflow v2's state machine.
	// Opening a still-"submitted" report (Get) auto-transitions it to
	// under_review; every other transition is its own dedicated action
	// route rather than a generic status setter.
	mux.Handle("GET /api/v1/admin/reports", requireAdmin(novelReportHandler.List))
	mux.Handle("GET /api/v1/admin/reports/{id}", requireAdmin(novelReportHandler.Get))
	mux.Handle("POST /api/v1/admin/reports/{id}/reject", requireAdmin(novelReportHandler.Reject))
	mux.Handle("POST /api/v1/admin/reports/{id}/resolve", requireAdmin(novelReportHandler.ResolveDirect))
	mux.Handle("POST /api/v1/admin/reports/{id}/hold-chapter", requireAdmin(novelReportHandler.HoldChapter))
	mux.Handle("POST /api/v1/admin/reports/{id}/hold-novel", requireAdmin(novelReportHandler.HoldNovel))
	mux.Handle("GET /api/v1/admin/reports/{id}/moderation-actions", requireAdmin(novelReportHandler.ModerationActions))
	mux.Handle("GET /api/v1/admin/reports/{id}/release-requests", requireAdmin(novelReportHandler.ReleaseRequestsAdmin))
	mux.Handle("POST /api/v1/admin/reports/{id}/release-requests/{requestId}/approve", requireAdmin(novelReportHandler.ApproveRelease))
	mux.Handle("POST /api/v1/admin/reports/{id}/release-requests/{requestId}/reject", requireAdmin(novelReportHandler.RejectRelease))
	mux.Handle("DELETE /api/v1/admin/reports/bulk", requireAdmin(novelReportHandler.AdminBulkDelete))
	mux.Handle("DELETE /api/v1/admin/reports/{id}", requireAdmin(novelReportHandler.AdminDelete))

	// Admin: author moderation (report detail view's author panel — see
	// AuthorModerationService's doc comment on why this is name-keyed)
	mux.Handle("GET /api/v1/admin/authors/{authorName}/novels", requireAdmin(authorModerationHandler.OtherNovels))
	mux.Handle("GET /api/v1/admin/authors/{authorName}/strikes", requireAdmin(authorModerationHandler.Strikes))
	mux.Handle("POST /api/v1/admin/authors/{authorName}/strikes", requireAdmin(authorModerationHandler.AddStrike))
	mux.Handle("POST /api/v1/admin/authors/{authorName}/hide-novels", requireAdmin(authorModerationHandler.BulkHide))
	mux.Handle("GET /api/v1/admin/authors/{authorName}/reports", requireAdmin(authorModerationHandler.Reports))

	// Admin: genres
	mux.Handle("GET /api/v1/admin/genres", requireAdmin(genreHandler.List))
	mux.Handle("POST /api/v1/admin/genres", requireAdmin(genreHandler.Create))
	mux.Handle("PUT /api/v1/admin/genres/{id}", requireAdmin(genreHandler.Update))
	mux.Handle("DELETE /api/v1/admin/genres/{id}", requireAdmin(genreHandler.Delete))

	// Admin: discover sections (Section Registry) + per-section overrides
	mux.Handle("GET /api/v1/admin/discover-sections", requireAdmin(discoverSectionHandler.List))
	mux.Handle("POST /api/v1/admin/discover-sections", requireAdmin(discoverSectionHandler.Create))
	mux.Handle("PUT /api/v1/admin/discover-sections/{key}", requireAdmin(discoverSectionHandler.Update))
	mux.Handle("DELETE /api/v1/admin/discover-sections/{key}", requireAdmin(discoverSectionHandler.Delete))
	mux.Handle("GET /api/v1/admin/discover-sections/{key}/overrides", requireAdmin(discoverSectionHandler.ListOverrides))
	mux.Handle("GET /api/v1/admin/discover-sections/{key}/preview", requireAdmin(discoverSectionHandler.Preview))
	mux.Handle("PUT /api/v1/admin/discover-sections/{key}/overrides/{novelId}", requireAdmin(discoverSectionHandler.SetOverride))
	mux.Handle("DELETE /api/v1/admin/discover-sections/{key}/overrides/{novelId}", requireAdmin(discoverSectionHandler.RemoveOverride))
	mux.Handle("POST /api/v1/admin/discover-sections/suggest", requireAdmin(discoverSectionHandler.Suggest))
	mux.Handle("GET /api/v1/admin/novels/{id}/section-overrides", requireAdmin(discoverSectionHandler.NovelOverrides))

	// Admin: users
	mux.Handle("GET /api/v1/admin/users", requireAdmin(adminUserHandler.List))
	mux.Handle("PUT /api/v1/admin/users/{id}/role", requireAdmin(adminUserHandler.UpdateRole))
	mux.Handle("PUT /api/v1/admin/users/{id}/ban", requireAdmin(adminUserHandler.UpdateBanned))

	// Admin: overview stats
	mux.Handle("GET /api/v1/admin/stats", requireAdmin(adminStatsHandler.Get))
	mux.Handle("GET /api/v1/admin/stats/series", requireAdmin(adminStatsHandler.Series))

	// Admin: audit log
	mux.Handle("GET /api/v1/admin/audit-logs", requireAdmin(adminAuditLogHandler.List))

	// Admin: notification composer
	mux.Handle("POST /api/v1/admin/notifications/broadcast", requireAdmin(notificationHandler.Broadcast))

	// Admin: chapters
	mux.Handle("GET /api/v1/admin/novels/{id}/chapters", requireAdmin(adminChapterHandler.ListByNovel))
	mux.Handle("POST /api/v1/admin/novels/{id}/chapters", requireAdmin(adminChapterHandler.Create))
	mux.Handle("POST /api/v1/admin/novels/{id}/chapters/import", requireAdmin(adminChapterHandler.Import))
	mux.Handle("GET /api/v1/admin/chapters/{id}", requireAdmin(adminChapterHandler.Get))
	mux.Handle("PUT /api/v1/admin/chapters/{id}", requireAdmin(adminChapterHandler.Update))
	mux.Handle("PUT /api/v1/admin/chapters/{id}/status", requireAdmin(adminChapterHandler.UpdateStatus))
	mux.Handle("PUT /api/v1/admin/chapters/{id}/schedule", requireAdmin(adminChapterHandler.Schedule))
	mux.Handle("DELETE /api/v1/admin/chapters/{id}", requireAdmin(adminChapterHandler.Delete))

	// Realtime events (JWT via ?token= — browsers can't set WS headers)
	mux.Handle("GET /ws", realtime.NewWSHandler(eventHub, configuration.JWTSecret))

	server := &http.Server{
		Addr:    ":" + configuration.Port,
		Handler: middleware.RequestLogger(rateLimitExcept(globalRateLimiter, "/uploads/", mux)),
	}

	log.Printf("novelora_backend listening on :%s", configuration.Port)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// rateLimitExcept applies limiter to every request except those whose
// path starts with skipPrefix. Static asset serving (cover images,
// evidence screenshots) is cheap, read-only, and a single content-heavy
// screen legitimately fires dozens of these in parallel — counting them
// against the same per-IP budget as real API calls made normal browsing
// trip the limiter (a book grid alone was seen sending ~100 cover
// requests in 6 seconds). The IPRateLimiter itself stays generic; this
// route-specific exemption lives here with the rest of the route wiring.
func rateLimitExcept(limiter *middleware.IPRateLimiter, skipPrefix string, next http.Handler) http.Handler {
	limited := limiter.Middleware(next)
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, skipPrefix) {
			next.ServeHTTP(responseWriter, request)
			return
		}
		limited.ServeHTTP(responseWriter, request)
	})
}

// runScheduledPublishTicker checks every minute for draft chapters
// whose scheduled_at has arrived and publishes them. A minute of
// slack on "publish at exactly HH:MM" is an acceptable trade for not
// needing a real job queue at this scale.
func runScheduledPublishTicker(chapterService *service.ChapterService) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		published, err := chapterService.PublishDueScheduled(ctx)
		cancel()
		if err != nil {
			log.Printf("schedule: check for due chapters failed: %v", err)
		} else if published > 0 {
			log.Printf("schedule: auto-published %d chapter(s)", published)
		}
	}
}

// runRankingDetectionTicker periodically checks whether any novel has
// newly entered a tracked ranked section (Trending, a category's own
// Most Read, ...) and notifies readers about it. 45 minutes is frequent
// enough to feel timely without a novel hovering right at the ranking
// boundary flapping in and out and re-notifying every cycle.
func runRankingDetectionTicker(rankingService *service.RankingNotificationService) {
	ticker := time.NewTicker(45 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		rankingService.DetectAndNotify(ctx)
		cancel()
	}
}

// runReportPurgeTicker hard-deletes reports soft-deleted more than 30
// days ago (see NovelReportService.AdminDelete/PurgeOldDeleted). Once a
// day is plenty — the 30-day threshold is never urgent to the hour.
func runReportPurgeTicker(reports *service.NovelReportService) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		purged, err := reports.PurgeOldDeleted(ctx)
		cancel()
		if err != nil {
			log.Printf("reports: purge old deleted failed: %v", err)
		} else if purged > 0 {
			log.Printf("reports: purged %d report(s) deleted more than 30 days ago", purged)
		}
	}
}

// runOutboxProcessorTicker drains durably-queued notifications (see
// OutboxProcessor) every few seconds. Short interval and short per-tick
// timeout are deliberate: this is the replacement for what used to be
// an instant fire-and-forget goroutine, so a long poll interval would
// be a regression in how quickly readers actually get notified.
func runOutboxProcessorTicker(processor *service.OutboxProcessor) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if _, err := processor.ProcessBatch(ctx); err != nil {
			log.Printf("outbox: process batch failed: %v", err)
		}
		cancel()
	}
}
