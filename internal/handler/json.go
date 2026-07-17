package handler

import (
	"encoding/json"
	"net/http"
)

// writeJSON serializes payload to the response with the given status code.
func writeJSON(responseWriter http.ResponseWriter, statusCode int, payload any) {
	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(statusCode)
	if err := json.NewEncoder(responseWriter).Encode(payload); err != nil {
		http.Error(responseWriter, "failed to encode response", http.StatusInternalServerError)
	}
}
