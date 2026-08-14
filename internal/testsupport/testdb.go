// Package testsupport wires up real dependencies (a real Postgres pool,
// real repositories and services) for integration tests. This codebase
// has no repository/service interfaces to fake behind — every
// repository and service is a concrete struct constructed straight
// against *pgxpool.Pool — so a test that exercises real business logic
// exercises the real database. Every test using this package is
// expected to clean up its own rows via t.Cleanup(); nothing here
// truncates tables for you.
package testsupport

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/audit"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/config"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/database"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/push"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/service"
)

// loadRepoRootEnv sets KEY=VALUE pairs from the repo-root .env into the
// process environment, skipping keys already set. `go test` sets the
// working directory to the package under test, not the repo root, so
// config.Load()'s own relative ".env" lookup silently finds nothing
// unless something locates the file explicitly — runtime.Caller gives
// this file's own path (internal/testsupport/testdb.go), two levels
// below the repo root, regardless of which package's tests are running.
func loadRepoRootEnv() {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return
	}
	envPath := filepath.Join(filepath.Dir(thisFile), "..", "..", ".env")

	file, err := os.Open(envPath)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
}

// NewPool connects to the same database internal/config.Load() and
// internal/database.Connect() use in a real run — same .env, same
// migrations already applied. Skips the test (not a hard failure) if
// DATABASE_URL/JWT_SECRET aren't configured, so this suite doesn't
// break a machine that hasn't set up a dev database.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	loadRepoRootEnv()
	configuration, err := config.Load()
	if err != nil {
		t.Skipf("skipping integration test: %v (needs DATABASE_URL/JWT_SECRET, e.g. via .env)", err)
	}

	pool, err := database.Connect(context.Background(), configuration.DatabaseURL)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// Services bundles the real services + repositories most integration
// tests need, wired exactly as cmd/server/main.go wires them.
type Services struct {
	Pool           *pgxpool.Pool
	Users          *repository.UserRepository
	AuthorProfiles *repository.AuthorProfileRepository
	Auth           *service.AuthService
	Novel          *service.NovelService
	Chapter        *service.ChapterService
	AuditLogger    *audit.Logger
}

func NewServices(t *testing.T, pool *pgxpool.Pool) *Services {
	t.Helper()
	loadRepoRootEnv()
	configuration, err := config.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	userRepository := repository.NewUserRepository(pool)
	refreshTokenRepository := repository.NewRefreshTokenRepository(pool)
	authorProfileRepository := repository.NewAuthorProfileRepository(pool)
	novelRepository := repository.NewNovelRepository(pool)
	genreRepository := repository.NewGenreRepository(pool)
	novelRatingRepository := repository.NewNovelRatingRepository(pool)
	novelSupportRepository := repository.NewNovelSupportRepository(pool)
	readingHistoryRepository := repository.NewReadingHistoryRepository(pool)
	deviceTokenRepository := repository.NewDeviceTokenRepository(pool)
	notificationRepository := repository.NewNotificationRepository(pool)
	chapterRepository := repository.NewChapterRepository(pool)
	auditLogRepository := repository.NewAuditLogRepository(pool)
	outboxRepository := repository.NewOutboxRepository(pool)

	// Publish() is a non-blocking channel send even without Run() ever
	// started — safe to leave un-started in tests, no goroutine needed.
	eventHub := realtime.NewHub()
	var notifier push.Notifier = push.NoopNotifier{}

	authService := service.NewAuthService(
		userRepository, refreshTokenRepository, authorProfileRepository, eventHub,
		configuration.JWTSecret, configuration.AccessTokenTTL, configuration.RefreshTokenTTL, configuration.GoogleClientID)
	novelService := service.NewNovelService(
		novelRepository, genreRepository, novelRatingRepository, novelSupportRepository,
		readingHistoryRepository, deviceTokenRepository, notificationRepository, notifier, eventHub,
		outboxRepository)
	chapterService := service.NewChapterService(
		chapterRepository, novelRepository, deviceTokenRepository, notificationRepository, notifier, eventHub,
		outboxRepository)

	return &Services{
		Pool:           pool,
		Users:          userRepository,
		AuthorProfiles: authorProfileRepository,
		Auth:           authService,
		Novel:          novelService,
		Chapter:        chapterService,
		AuditLogger:    audit.NewLogger(auditLogRepository),
	}
}

// NewTestAuthor creates a throwaway user with an author profile
// (unique username/email per call), registering cleanup that deletes
// the user — author_profiles cascades, any novel the test created
// under this user must be deleted by the test itself first (or accept
// it surviving with owner_user_id set NULL by the FK's ON DELETE SET
// NULL).
func NewTestAuthor(t *testing.T, ctx context.Context, services *Services, namePrefix string) (userID, penName string) {
	t.Helper()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	username := namePrefix + suffix
	user, err := services.Users.Create(ctx, username, username+"@novelora.test", "unused-test-hash")
	if err != nil {
		t.Fatalf("create test user %s: %v", username, err)
	}
	t.Cleanup(func() {
		if _, err := services.Pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", user.ID); err != nil {
			t.Logf("cleanup: delete test user %s: %v", user.ID, err)
		}
	})

	penName = "Pen " + suffix
	if _, err := services.AuthorProfiles.Create(ctx, user.ID, penName); err != nil {
		t.Fatalf("create test author profile for %s: %v", username, err)
	}
	return user.ID, penName
}
