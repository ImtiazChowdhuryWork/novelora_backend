package handler

import "net/http"

// Health reports service liveness.
func Health(responseWriter http.ResponseWriter, request *http.Request) {
	writeJSON(responseWriter, http.StatusOK, map[string]string{
		"status": "ok",
	})
}
