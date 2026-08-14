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
	"google.golang.org/api/idtoken"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/realtime"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/repository"
)

var (
	ErrInvalidCredentials       = errors.New("invalid email or password")
	ErrInvalidRefreshToken      = errors.New("refresh token is invalid or expired")
	ErrInvalidGoogleToken       = errors.New("google sign-in token is invalid")
	ErrGoogleLoginNotConfigured = errors.New("google sign-in is not configured on this server")
	ErrAccountBanned            = errors.New("this account has been suspended")
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
	authorProfiles  *repository.AuthorProfileRepository
	events          realtime.Publisher
	jwtSecret       []byte
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	googleClientID  string
}

func NewAuthService(
	users *repository.UserRepository,
	refreshTokens *repository.RefreshTokenRepository,
	authorProfiles *repository.AuthorProfileRepository,
	events realtime.Publisher,
	jwtSecret []byte,
	accessTokenTTL time.Duration,
	refreshTokenTTL time.Duration,
	googleClientID string,
) *AuthService {
	return &AuthService{
		users:           users,
		refreshTokens:   refreshTokens,
		authorProfiles:  authorProfiles,
		events:          events,
		jwtSecret:       jwtSecret,
		accessTokenTTL:  accessTokenTTL,
		refreshTokenTTL: refreshTokenTTL,
		googleClientID:  googleClientID,
	}
}

// AuthResult is what a successful register/login/refresh returns.
type AuthResult struct {
	User          *repository.User
	AuthorProfile *repository.AuthorProfile // nil when the user isn't an author
	AccessToken   string
	RefreshToken  string
	ExpiresIn     int64 // access-token lifetime in seconds
}

const maxPenNameLength = 100

// BecomeAuthor is the "become an author" upgrade — both the
// already-logged-in-reader-opts-in path and the direct-signup path
// (register, then call this immediately) funnel through here. Reuses
// issueTokens so the caller gets a fresh is_author:true access token
// without a separate re-login.
func (authService *AuthService) BecomeAuthor(ctx context.Context, userID, penName string) (*AuthResult, error) {
	penName = strings.TrimSpace(penName)
	if penName == "" {
		return nil, &ValidationError{Message: "pen name is required"}
	}
	if len(penName) > maxPenNameLength {
		return nil, &ValidationError{Message: fmt.Sprintf("pen name must be at most %d characters", maxPenNameLength)}
	}

	if _, err := authService.authorProfiles.Create(ctx, userID, penName); err != nil {
		return nil, err
	}

	user, err := authService.users.FindByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	return authService.issueTokens(ctx, user)
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
	authService.events.Publish(realtime.Event{Topic: "user.registered", ID: user.ID})
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

// LoginWithGoogle verifies a Google ID token and signs the account in,
// creating the user or linking the Google account as needed.
func (authService *AuthService) LoginWithGoogle(ctx context.Context, googleIDToken string) (*AuthResult, error) {
	if authService.googleClientID == "" {
		return nil, ErrGoogleLoginNotConfigured
	}

	payload, err := idtoken.Validate(ctx, googleIDToken, authService.googleClientID)
	if err != nil {
		return nil, ErrInvalidGoogleToken
	}

	googleAccountID := payload.Subject
	email, _ := payload.Claims["email"].(string)
	if googleAccountID == "" || email == "" {
		return nil, ErrInvalidGoogleToken
	}
	avatarURL, _ := payload.Claims["picture"].(string)

	user, err := authService.users.FindByGoogleID(ctx, googleAccountID)
	if err == nil {
		return authService.issueTokens(ctx, authService.syncAvatar(ctx, user, avatarURL))
	}
	if !errors.Is(err, repository.ErrUserNotFound) {
		return nil, err
	}

	// First Google sign-in: link to the existing account with the same
	// email, or create a fresh account.
	user, err = authService.users.FindByEmail(ctx, email)
	if err == nil {
		if err := authService.users.SetGoogleID(ctx, user.ID, googleAccountID); err != nil {
			return nil, err
		}
		return authService.issueTokens(ctx, authService.syncAvatar(ctx, user, avatarURL))
	}
	if !errors.Is(err, repository.ErrUserNotFound) {
		return nil, err
	}

	displayName, _ := payload.Claims["name"].(string)
	user, err = authService.createGoogleUser(ctx, email, displayName, googleAccountID, avatarURL)
	if err != nil {
		return nil, err
	}
	authService.events.Publish(realtime.Event{Topic: "user.registered", ID: user.ID})
	return authService.issueTokens(ctx, user)
}

// syncAvatar keeps the stored avatar in step with the Google profile
// picture. Failures are non-fatal: sign-in proceeds with the old avatar.
func (authService *AuthService) syncAvatar(ctx context.Context, user *repository.User, avatarURL string) *repository.User {
	if avatarURL == "" || avatarURL == user.AvatarURL {
		return user
	}
	if err := authService.users.UpdateAvatarURL(ctx, user.ID, avatarURL); err != nil {
		return user
	}
	user.AvatarURL = avatarURL
	return user
}

func (authService *AuthService) createGoogleUser(ctx context.Context, email, displayName, googleAccountID, avatarURL string) (*repository.User, error) {
	baseUsername := usernameFromGoogleProfile(email, displayName)
	candidate := baseUsername
	for attempt := 0; attempt < 6; attempt++ {
		user, err := authService.users.CreateWithGoogle(ctx, candidate, email, googleAccountID, avatarURL)
		if err == nil {
			return user, nil
		}
		if !errors.Is(err, repository.ErrUsernameTaken) {
			return nil, err
		}
		suffixBytes := make([]byte, 2)
		if _, err := rand.Read(suffixBytes); err != nil {
			return nil, fmt.Errorf("generate username suffix: %w", err)
		}
		suffix := hex.EncodeToString(suffixBytes)
		candidate = trimToLength(baseUsername, maxUsernameLength-len(suffix)) + suffix
	}
	return nil, fmt.Errorf("could not find a free username for %s", email)
}

// usernameFromGoogleProfile builds a valid username from the Google
// display name (preferred) or the email prefix.
func usernameFromGoogleProfile(email, displayName string) string {
	source := displayName
	if source == "" {
		source, _, _ = strings.Cut(email, "@")
	}
	var builder strings.Builder
	for _, character := range strings.ToLower(source) {
		switch {
		case character >= 'a' && character <= 'z',
			character >= '0' && character <= '9',
			character == '_':
			builder.WriteRune(character)
		case character == ' ' || character == '.' || character == '-':
			builder.WriteRune('_')
		}
	}
	username := builder.String()
	if len(username) < minUsernameLength {
		username += "_reader"
	}
	return trimToLength(username, maxUsernameLength)
}

func trimToLength(value string, maxLength int) string {
	if len(value) > maxLength {
		return value[:maxLength]
	}
	return value
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

// issueTokens is the single choke point every sign-in path (register,
// login, Google, refresh) funnels through, so the ban check only needs
// to live here — a freshly registered user is never banned, so the
// check is a no-op on that path.
func (authService *AuthService) issueTokens(ctx context.Context, user *repository.User) (*AuthResult, error) {
	if user.IsBanned {
		return nil, ErrAccountBanned
	}

	authorProfile, err := authService.authorProfiles.GetByUserID(ctx, user.ID)
	if err != nil && !errors.Is(err, repository.ErrAuthorProfileNotFound) {
		return nil, err
	}
	isAuthor := authorProfile != nil

	now := time.Now()

	accessToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":       user.ID,
		"name":      user.Username,
		"role":      user.Role,
		"is_author": isAuthor,
		"iat":       now.Unix(),
		"exp":       now.Add(authService.accessTokenTTL).Unix(),
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
		User:          user,
		AuthorProfile: authorProfile,
		AccessToken:   accessToken,
		RefreshToken:  refreshToken,
		ExpiresIn:     int64(authService.accessTokenTTL.Seconds()),
	}, nil
}

// hashRefreshToken maps the client-held token to its stored form; raw
// refresh tokens never touch the database.
func hashRefreshToken(refreshToken string) string {
	digest := sha256.Sum256([]byte(refreshToken))
	return hex.EncodeToString(digest[:])
}
