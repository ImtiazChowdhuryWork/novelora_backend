package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds all runtime configuration, sourced from environment
// variables (a .env file in the working directory fills in anything
// not already set — local development convenience).
type Config struct {
	Port            string
	DatabaseURL     string
	JWTSecret       []byte
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	// OAuth Web client ID used as the audience of Google ID tokens.
	// Empty disables Google sign-in (endpoint answers 503).
	GoogleClientID string
	// Root directory for uploaded files (avatars live in a subfolder).
	UploadsDirectory string
	// Path to a Firebase service account JSON key (Firebase Console →
	// Project Settings → Service Accounts → Generate new private key).
	// Empty disables push notifications (chapters still publish; the
	// notification step is skipped and logged).
	FirebaseCredentialsPath string
}

// Load reads configuration from the environment.
func Load() (*Config, error) {
	loadDotEnv(".env")

	configuration := &Config{
		Port:                    envOrDefault("PORT", "8080"),
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		AccessTokenTTL:          15 * time.Minute,
		RefreshTokenTTL:         30 * 24 * time.Hour,
		GoogleClientID:          os.Getenv("GOOGLE_CLIENT_ID"),
		UploadsDirectory:        envOrDefault("UPLOADS_DIR", "uploads"),
		FirebaseCredentialsPath: os.Getenv("FIREBASE_CREDENTIALS_JSON"),
	}

	if configuration.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		return nil, fmt.Errorf("JWT_SECRET is not set")
	}
	configuration.JWTSecret = []byte(jwtSecret)

	return configuration, nil
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// loadDotEnv sets KEY=VALUE pairs from the given file into the process
// environment, skipping keys that are already set and lines that are
// blank or comments. A missing file is not an error.
func loadDotEnv(path string) {
	file, err := os.Open(path)
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
