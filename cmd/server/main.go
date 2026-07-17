package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/config"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/database"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/handler"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
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
	authService := service.NewAuthService(
		userRepository,
		refreshTokenRepository,
		configuration.JWTSecret,
		configuration.AccessTokenTTL,
		configuration.RefreshTokenTTL,
	)
	authHandler := handler.NewAuthHandler(authService)

	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /health", handler.Health)

	// Auth (matches the Flutter app's ApiEndpoints)
	mux.HandleFunc("POST /api/v1/auth/register", authHandler.Register)
	mux.HandleFunc("POST /api/v1/auth/login", authHandler.Login)
	mux.HandleFunc("POST /api/v1/auth/refresh", authHandler.RefreshToken)
	mux.HandleFunc("POST /api/v1/auth/logout", authHandler.Logout)

	server := &http.Server{
		Addr:    ":" + configuration.Port,
		Handler: middleware.RequestLogger(mux),
	}

	log.Printf("novelora_backend listening on :%s", configuration.Port)
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
