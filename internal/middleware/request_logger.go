package middleware

import (
	"log"
	"net/http"
	"time"
)

// RequestLogger logs method, path, and duration of every request.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		next.ServeHTTP(responseWriter, request)
		log.Printf("%s %s (%s)", request.Method, request.URL.Path, time.Since(startedAt))
	})
}
