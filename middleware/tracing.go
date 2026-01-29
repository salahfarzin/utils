package middleware

import (
	"net/http"

	"github.com/salahfarzin/utils/tracing"
)

// TracingMiddleware extracts TraceID and UserID from headers and injects them into context.
// It also sets the TraceID in the response header.
func TracingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := tracing.GetOrGenerateTraceIDFromHeader(r)
		userID := tracing.GetUserIDFromHeader(r)

		ctx := r.Context()
		ctx = tracing.InjectTraceIDToContext(ctx, traceID)
		if userID != "" {
			ctx = tracing.InjectUserIDToContext(ctx, userID)
		}

		tracing.SetTraceIDHeader(w, traceID)
		if userID != "" {
			tracing.SetUserIDHeader(w, userID)
		}

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
