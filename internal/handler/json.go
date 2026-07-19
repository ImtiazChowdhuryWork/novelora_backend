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

// decodeJSONWithLimit parses the request body into destination; on
// failure it writes a 400 and returns false.
func decodeJSONWithLimit(responseWriter http.ResponseWriter, request *http.Request, destination any, maxBytes int64) bool {
	request.Body = http.MaxBytesReader(responseWriter, request.Body, maxBytes)
	if err := json.NewDecoder(request.Body).Decode(destination); err != nil {
		writeError(responseWriter, http.StatusBadRequest, "request body is not valid JSON (or too large)")
		return false
	}
	return true
}

// decodeLargeJSON accepts bulk payloads — a chapter import carries an
// entire book's text.
func decodeLargeJSON(responseWriter http.ResponseWriter, request *http.Request, destination any) bool {
	return decodeJSONWithLimit(responseWriter, request, destination, 64<<20)
}
