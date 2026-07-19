package main

import (
	"context"
	"log"
	"net/http"
	"path/filepath"
	"time"

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

	eventHub := realtime.NewHub()
	go eventHub.Run()

	authService := service.NewAuthService(
		userRepository,
		refreshTokenRepository,
		eventHub,
		configuration.JWTSecret,
		configuration.AccessTokenTTL,
		configuration.RefreshTokenTTL,
		configuration.GoogleClientID,
	)
	novelRepository := repository.NewNovelRepository(pool)
	genreRepository := repository.NewGenreRepository(pool)
	novelService := service.NewNovelService(novelRepository, genreRepository, eventHub)
	chapterRepository := repository.NewChapterRepository(pool)
	deviceTokenRepository := repository.NewDeviceTokenRepository(pool)
	notificationRepository := repository.NewNotificationRepository(pool)

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

	chapterService := service.NewChapterService(
		chapterRepository, novelRepository, deviceTokenRepository, notificationRepository, chapterNotifier, eventHub)

	authHandler := handler.NewAuthHandler(authService)
	userHandler := handler.NewUserHandler(
		userRepository, deviceTokenRepository, configuration.JWTSecret,
		filepath.Join(configuration.UploadsDirectory, "avatars"))
	adminNovelHandler := handler.NewAdminNovelHandler(novelService, configuration.UploadsDirectory)
	adminChapterHandler := handler.NewAdminChapterHandler(chapterService)
	publicNovelHandler := handler.NewPublicNovelHandler(novelService, chapterService)
	notificationHandler := handler.NewNotificationHandler(notificationRepository)
	genreHandler := handler.NewGenreHandler(genreRepository)
	adminUserHandler := handler.NewAdminUserHandler(userRepository)
	adminStatsHandler := handler.NewAdminStatsHandler(repository.NewStatsRepository(pool))

	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /health", handler.Health)

	// Auth (matches the Flutter app's ApiEndpoints)
	mux.HandleFunc("POST /api/v1/auth/register", authHandler.Register)
	mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	mux.HandleFunc("POST /api/v1/auth/google", authHandler.GoogleLogin)
	mux.HandleFunc("POST /api/v1/auth/refresh", authHandler.RefreshToken)
	mux.HandleFunc("POST /api/v1/auth/logout", authHandler.Logout)

	// Users (require a valid access token)
	mux.Handle("GET /api/v1/users/me", middleware.Authenticate(
		configuration.JWTSecret, http.HandlerFunc(userHandler.CurrentUser)))
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

	// Uploaded files (avatars)
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/",
		http.FileServer(http.Dir(configuration.UploadsDirectory))))

	// Public catalog (reader-facing; published content only)
	mux.HandleFunc("GET /api/v1/novels", publicNovelHandler.List)
	mux.HandleFunc("GET /api/v1/novels/{id}", publicNovelHandler.Get)
	mux.HandleFunc("GET /api/v1/novels/{id}/chapters", publicNovelHandler.Chapters)
	mux.HandleFunc("GET /api/v1/chapters/{id}", publicNovelHandler.Chapter)
	mux.HandleFunc("GET /api/v1/genres", genreHandler.List)

	// Admin: novels (JWT + admin role)
	requireAdmin := func(handlerFunc http.HandlerFunc) http.Handler {
		return middleware.Authenticate(configuration.JWTSecret,
			middleware.RequireAdmin(handlerFunc))
	}
	mux.Handle("GET /api/v1/admin/novels", requireAdmin(adminNovelHandler.List))
	mux.Handle("POST /api/v1/admin/novels", requireAdmin(adminNovelHandler.Create))
	mux.Handle("GET /api/v1/admin/novels/{id}", requireAdmin(adminNovelHandler.Get))
	mux.Handle("PUT /api/v1/admin/novels/{id}", requireAdmin(adminNovelHandler.Update))
	mux.Handle("DELETE /api/v1/admin/novels/{id}", requireAdmin(adminNovelHandler.Delete))
	mux.Handle("PUT /api/v1/admin/novels/{id}/cover", requireAdmin(adminNovelHandler.UpdateCover))

	// Admin: genres
	mux.Handle("GET /api/v1/admin/genres", requireAdmin(genreHandler.List))
	mux.Handle("POST /api/v1/admin/genres", requireAdmin(genreHandler.Create))
	mux.Handle("DELETE /api/v1/admin/genres/{id}", requireAdmin(genreHandler.Delete))

	// Admin: users
	mux.Handle("GET /api/v1/admin/users", requireAdmin(adminUserHandler.List))
	mux.Handle("PUT /api/v1/admin/users/{id}/role", requireAdmin(adminUserHandler.UpdateRole))
	mux.Handle("PUT /api/v1/admin/users/{id}/ban", requireAdmin(adminUserHandler.UpdateBanned))

	// Admin: overview stats
	mux.Handle("GET /api/v1/admin/stats", requireAdmin(adminStatsHandler.Get))

	// Admin: chapters
	mux.Handle("GET /api/v1/admin/novels/{id}/chapters", requireAdmin(adminChapterHandler.ListByNovel))
	mux.Handle("POST /api/v1/admin/novels/{id}/chapters", requireAdmin(adminChapterHandler.Create))
	mux.Handle("POST /api/v1/admin/novels/{id}/chapters/import", requireAdmin(adminChapterHandler.Import))
	mux.Handle("GET /api/v1/admin/chapters/{id}", requireAdmin(adminChapterHandler.Get))
	mux.Handle("PUT /api/v1/admin/chapters/{id}", requireAdmin(adminChapterHandler.Update))
	mux.Handle("PUT /api/v1/admin/chapters/{id}/status", requireAdmin(adminChapterHandler.UpdateStatus))
	mux.Handle("DELETE /api/v1/admin/chapters/{id}", requireAdmin(adminChapterHandler.Delete))

	// Realtime events (JWT via ?token= — browsers can't set WS headers)
	mux.Handle("GET /ws", realtime.NewWSHandler(eventHub, configuration.JWTSecret))

	server := &http.Server{
		Addr:    ":" + configuration.Port,
		Handler: middleware.RequestLogger(mux),
	}

	log.Printf("novelora_backend listening on :%s", configuration.Port)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
