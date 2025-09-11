package utils

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
	"google.golang.org/grpc/metadata"
)

type ctxKey string

const (
	TraceIDKey ctxKey = "trace_id"
	UserIDKey  ctxKey = "user_id"
)

type ErrorResponse struct {
	Error   string `json:"error"`
	TraceID string `json:"trace_id,omitempty"`
}

// GetOrGenerateTraceID tries to extract a trace ID from gRPC metadata, or generates a new one if not present.
func GetOrGenerateTraceID(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-trace-id"); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}
	return uuid.New().String()
}

// GetUserIDFromContext tries to extract a user ID from gRPC metadata.
func GetUserIDFromContext(ctx context.Context) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-user-id"); len(vals) > 0 && vals[0] != "" {
			return vals[0]
		}
	}
	return ""
}

// GetOrGenerateTraceIDFromHeader extracts trace ID from HTTP headers or generates a new one.
func GetOrGenerateTraceIDFromHeader(r *http.Request) string {
	traceID := r.Header.Get("X-Trace-Id")
	if traceID != "" {
		return traceID
	}
	return uuid.New().String()
}

// GetUserIDFromHeader extracts user ID from HTTP headers.
func GetUserIDFromHeader(r *http.Request) string {
	return r.Header.Get("X-User-Id")
}

// SetTraceIDHeader sets the trace ID in HTTP response headers.
func SetTraceIDHeader(w http.ResponseWriter, traceID string) {
	w.Header().Set("X-Trace-Id", traceID)
}

// SetUserIDHeader sets the user ID in HTTP response headers.
func SetUserIDHeader(w http.ResponseWriter, userID string) {
	w.Header().Set("X-User-Id", userID)
}

// WriteJSONError writes a standardized JSON error response for REST APIs.
func WriteJSONError(w http.ResponseWriter, status int, errMsg, traceID string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ErrorResponse{Error: errMsg, TraceID: traceID})
}

// InjectTraceIDToContext returns a new context with the trace ID.
func InjectTraceIDToContext(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, TraceIDKey, traceID)
}

// InjectUserIDToContext returns a new context with the user ID.
func InjectUserIDToContext(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserIDKey, userID)
}

// GetTraceIDFromContext extracts the trace ID from context (for both REST and gRPC).
func GetTraceIDFromContext(ctx context.Context) string {
	if v := ctx.Value(TraceIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return GetOrGenerateTraceID(ctx)
}

// GetUserIDFromContextGeneric extracts the user ID from context (for both REST and gRPC).
func GetUserIDFromContextGeneric(ctx context.Context) string {
	if v := ctx.Value(UserIDKey); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return GetUserIDFromContext(ctx)
}
