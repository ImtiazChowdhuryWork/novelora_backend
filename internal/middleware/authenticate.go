package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

// UserIDContextKey holds the authenticated user's id in the request context.
const UserIDContextKey contextKey = "userID"

// UserRoleContextKey holds the authenticated user's role in the request context.
const UserRoleContextKey contextKey = "userRole"

// UserNameContextKey holds the authenticated user's username in the
// request context — mainly for audit logging, so it doesn't need a
// separate DB lookup per action.
const UserNameContextKey contextKey = "userName"

// IsAuthorContextKey holds whether the authenticated user has an author
// profile — an additive capability alongside role, not a role value
// itself (a user stays "reader" or "admin" and can independently also
// be an author; see author_profiles).
const IsAuthorContextKey contextKey = "isAuthor"

// AccessTokenClaims is what a validated access token asserts.
type AccessTokenClaims struct {
	UserID   string
	Role     string
	Name     string
	IsAuthor bool
}

// ValidateAccessToken verifies an HS256 access token and returns its
// claims. Shared by the HTTP middleware and the WebSocket handshake.
func ValidateAccessToken(jwtSecret []byte, tokenString string) (*AccessTokenClaims, error) {
	parsedToken, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, errors.New("unexpected signing method")
		}
		return jwtSecret, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsedToken.Valid {
		return nil, errors.New("access token is invalid or expired")
	}

	subject, err := parsedToken.Claims.GetSubject()
	if err != nil || subject == "" {
		return nil, errors.New("access token is invalid or expired")
	}

	role := ""
	name := ""
	isAuthor := false
	if mapClaims, ok := parsedToken.Claims.(jwt.MapClaims); ok {
		role, _ = mapClaims["role"].(string)
		name, _ = mapClaims["name"].(string)
		isAuthor, _ = mapClaims["is_author"].(bool)
	}

	return &AccessTokenClaims{UserID: subject, Role: role, Name: name, IsAuthor: isAuthor}, nil
}

// Authenticate validates the Bearer access token and stores the user id
// and role in the request context for downstream handlers.
func Authenticate(jwtSecret []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		authorizationHeader := request.Header.Get("Authorization")
		tokenString, hasBearerPrefix := strings.CutPrefix(authorizationHeader, "Bearer ")
		if !hasBearerPrefix || tokenString == "" {
			writeAuthError(responseWriter, http.StatusUnauthorized, "missing bearer token")
			return
		}

		claims, err := ValidateAccessToken(jwtSecret, tokenString)
		if err != nil {
			writeAuthError(responseWriter, http.StatusUnauthorized, err.Error())
			return
		}

		requestContext := context.WithValue(request.Context(), UserIDContextKey, claims.UserID)
		requestContext = context.WithValue(requestContext, UserRoleContextKey, claims.Role)
		requestContext = context.WithValue(requestContext, UserNameContextKey, claims.Name)
		requestContext = context.WithValue(requestContext, IsAuthorContextKey, claims.IsAuthor)
		next.ServeHTTP(responseWriter, request.WithContext(requestContext))
	})
}

// RequireAdmin allows the request through only for admin users.
// Must be wrapped by Authenticate (it reads the role from the context).
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		role, _ := request.Context().Value(UserRoleContextKey).(string)
		if role != "admin" {
			writeAuthError(responseWriter, http.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(responseWriter, request)
	})
}

// RequireAuthor allows the request through only for users with an
// author profile. Must be wrapped by Authenticate (it reads is_author
// from the context) — unlike RequireAdmin, this checks a capability
// flag, not the mutually-exclusive role value, since a user stays
// "reader" and can independently also be an author.
func RequireAuthor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		isAuthor, _ := request.Context().Value(IsAuthorContextKey).(bool)
		if !isAuthor {
			writeAuthError(responseWriter, http.StatusForbidden, "author access required")
			return
		}
		next.ServeHTTP(responseWriter, request)
	})
}

// OptionalUserID resolves the caller from a Bearer token if one is
// present and valid; a missing or invalid token is not an error here
// (unlike Authenticate) — it just means an anonymous/guest request.
// For routes that serve both guests and logged-in readers the same
// content, but want to attribute the request to an account when one is
// available (e.g. recording reading history on chapter open).
func OptionalUserID(jwtSecret []byte, request *http.Request) (userID string, ok bool) {
	tokenString, hasBearerPrefix := strings.CutPrefix(request.Header.Get("Authorization"), "Bearer ")
	if !hasBearerPrefix || tokenString == "" {
		return "", false
	}
	claims, err := ValidateAccessToken(jwtSecret, tokenString)
	if err != nil {
		return "", false
	}
	return claims.UserID, true
}

func writeAuthError(responseWriter http.ResponseWriter, statusCode int, message string) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	responseWriter.Write([]byte(`{"error":"` + message + `"}`))
}
