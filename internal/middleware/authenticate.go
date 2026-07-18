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

// Authenticate validates the Bearer access token and stores the user id
// in the request context for downstream handlers.
func Authenticate(jwtSecret []byte, next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		authorizationHeader := request.Header.Get("Authorization")
		tokenString, hasBearerPrefix := strings.CutPrefix(authorizationHeader, "Bearer ")
		if !hasBearerPrefix || tokenString == "" {
			writeUnauthorized(responseWriter, "missing bearer token")
			return
		}

		parsedToken, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, errors.New("unexpected signing method")
			}
			return jwtSecret, nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !parsedToken.Valid {
			writeUnauthorized(responseWriter, "access token is invalid or expired")
			return
		}

		subject, err := parsedToken.Claims.GetSubject()
		if err != nil || subject == "" {
			writeUnauthorized(responseWriter, "access token is invalid or expired")
			return
		}

		requestContext := context.WithValue(request.Context(), UserIDContextKey, subject)
		next.ServeHTTP(responseWriter, request.WithContext(requestContext))
	})
}

func writeUnauthorized(responseWriter http.ResponseWriter, message string) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(http.StatusUnauthorized)
	responseWriter.Write([]byte(`{"error":"` + message + `"}`))
}
