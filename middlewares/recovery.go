package middlewares

import (
	"net/http"
	"runtime/debug"

	"github.com/salahfarzin/logger"
	"github.com/salahfarzin/utils/rest"
	"github.com/salahfarzin/utils/tracing"
	"go.uber.org/zap"
)

// RecoveryMiddleware recovers from panics and logs the stack trace.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log := logger.Get()
				traceID := tracing.GetTraceIDFromContext(r.Context())

				log.Error("Request panic recovered",
					zap.Any("error", err),
					zap.String("trace_id", traceID),
					zap.String("stack", string(debug.Stack())),
				)

				rest.WriteJSONError(w, http.StatusInternalServerError, "Internal Server Error", traceID)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
