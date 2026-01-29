package rest

import (
	"encoding/json"
	"net/http"
)

type ErrorResponse struct {
	Error   string `json:"error"`
	TraceID string `json:"trace_id,omitempty"`
}

// WriteJSONError writes a standardized JSON error response for REST APIs.
func WriteJSONError(w http.ResponseWriter, status int, errMsg, traceID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{Error: errMsg, TraceID: traceID})
}
