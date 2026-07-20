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

// AccessTokenClaims is what a validated access token asserts.
type AccessTokenClaims struct {
	UserID string
	Role   string
	Name   string
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
	if mapClaims, ok := parsedToken.Claims.(jwt.MapClaims); ok {
		role, _ = mapClaims["role"].(string)
		name, _ = mapClaims["name"].(string)
	}

	return &AccessTokenClaims{UserID: subject, Role: role, Name: name}, nil
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

func writeAuthError(responseWriter http.ResponseWriter, statusCode int, message string) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	responseWriter.Write([]byte(`{"error":"` + message + `"}`))
}
