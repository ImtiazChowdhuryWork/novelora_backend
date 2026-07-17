package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

var (
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrInvalidRefreshToken = errors.New("refresh token is invalid or expired")
)

// ValidationError reports invalid user input; handlers map it to 400.
type ValidationError struct {
	Message string
}

func (validationError *ValidationError) Error() string {
	return validationError.Message
}

// Validation limits mirror AppConstants in the Flutter app.
const (
	minUsernameLength = 3
	maxUsernameLength = 50
	minPasswordLength = 8
	maxPasswordLength = 128
)

type AuthService struct {
	users           *repository.UserRepository
	refreshTokens   *repository.RefreshTokenRepository
	jwtSecret       []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
}

func NewAuthService(
	users *repository.UserRepository,
	refreshTokens *repository.RefreshTokenRepository,
	jwtSecret []byte,
	accessTokenTTL time.Duration,
	refreshTokenTTL time.Duration,
) *AuthService {
	return &AuthService{
		users:           users,
		refreshTokens:   refreshTokens,
		jwtSecret:       jwtSecret,
		accessTokenTTL:  accessTokenTTL,
		refreshTokenTTL: refreshTokenTTL,
	}
}

// AuthResult is what a successful register/login/refresh returns.
type AuthResult struct {
	User         *repository.User
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64 // access-token lifetime in seconds
}

func (authService *AuthService) Register(ctx context.Context, username, email, password string) (*AuthResult, error) {
	username = strings.TrimSpace(username)
	email = strings.TrimSpace(email)

	if length := len(username); length < minUsernameLength || length > maxUsernameLength {
		return nil, &ValidationError{Message: fmt.Sprintf("username must be %d-%d characters", minUsernameLength, maxUsernameLength)}
	}
	if !strings.Contains(email, "@") || strings.ContainsAny(email, " \t") {
		return nil, &ValidationError{Message: "email address is not valid"}
	}
	if length := len(password); length < minPasswordLength || length > maxPasswordLength {
		return nil, &ValidationError{Message: fmt.Sprintf("password must be %d-%d characters", minPasswordLength, maxPasswordLength)}
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	user, err := authService.users.Create(ctx, username, email, string(passwordHash))
	if err != nil {
		return nil, err
	}
	return authService.issueTokens(ctx, user)
}

func (authService *AuthService) Login(ctx context.Context, email, password string) (*AuthResult, error) {
	user, err := authService.users.FindByEmail(ctx, strings.TrimSpace(email))
	if errors.Is(err, repository.ErrUserNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	return authService.issueTokens(ctx, user)
}

// Refresh rotates the refresh token: the presented token is revoked and
// a fresh pair is issued.
func (authService *AuthService) Refresh(ctx context.Context, refreshToken string) (*AuthResult, error) {
	tokenHash := hashRefreshToken(refreshToken)

	storedToken, err := authService.refreshTokens.FindActive(ctx, tokenHash)
	if errors.Is(err, repository.ErrRefreshTokenNotFound) {
		return nil, ErrInvalidRefreshToken
	}
	if err != nil {
		return nil, err
	}

	user, err := authService.users.FindByID(ctx, storedToken.UserID)
	if err != nil {
		return nil, err
	}

	if err := authService.refreshTokens.Revoke(ctx, tokenHash); err != nil {
		return nil, err
	}
	return authService.issueTokens(ctx, user)
}

func (authService *AuthService) Logout(ctx context.Context, refreshToken string) error {
	return authService.refreshTokens.Revoke(ctx, hashRefreshToken(refreshToken))
}

func (authService *AuthService) issueTokens(ctx context.Context, user *repository.User) (*AuthResult, error) {
	now := time.Now()

	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":  user.ID,
		"name": user.Username,
		"iat":  now.Unix(),
		"exp":  now.Add(authService.accessTokenTTL).Unix(),
	}).SignedString(authService.jwtSecret)
	if err != nil {
		return nil, fmt.Errorf("sign access token: %w", err)
	}

	refreshTokenBytes := make([]byte, 32)
	if _, err := rand.Read(refreshTokenBytes); err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}
	refreshToken := hex.EncodeToString(refreshTokenBytes)

	expiresAt := now.Add(authService.refreshTokenTTL)
	if err := authService.refreshTokens.Store(ctx, user.ID, hashRefreshToken(refreshToken), expiresAt); err != nil {
		return nil, err
	}

	return &AuthResult{
		User:         user,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresIn:    int64(authService.accessTokenTTL.Seconds()),
	}, nil
}

// hashRefreshToken maps the client-held token to its stored form; raw
// refresh tokens never touch the database.
func hashRefreshToken(refreshToken string) string {
	digest := sha256.Sum256([]byte(refreshToken))
	return hex.EncodeToString(digest[:])
}
