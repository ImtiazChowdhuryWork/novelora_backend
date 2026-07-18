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
	authHandler := handler.NewAuthHandler(authService)
	userHandler := handler.NewUserHandler(
		userRepository, filepath.Join(configuration.UploadsDirectory, "avatars"))

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

	// Uploaded files (avatars)
	mux.Handle("GET /uploads/", http.StripPrefix("/uploads/",
		http.FileServer(http.Dir(configuration.UploadsDirectory))))

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
