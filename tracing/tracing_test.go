package tracing

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func TestGetOrGenerateTraceID(t *testing.T) {
	t.Run("Generate new ID when no metadata", func(t *testing.T) {
		ctx := context.Background()
		id := GetOrGenerateTraceID(ctx)
		assert.NotEmpty(t, id)
	})

	t.Run("Extract ID from gRPC metadata", func(t *testing.T) {
		expectedID := "test-trace-id"
		md := metadata.Pairs("x-trace-id", expectedID)
		ctx := metadata.NewIncomingContext(context.Background(), md)
		id := GetOrGenerateTraceID(ctx)
		assert.Equal(t, expectedID, id)
	})
}

func TestGetUserIDFromContext(t *testing.T) {
	t.Run("Return empty when no metadata", func(t *testing.T) {
		ctx := context.Background()
		id := GetUserIDFromContext(ctx)
		assert.Empty(t, id)
	})

	t.Run("Extract ID from gRPC metadata", func(t *testing.T) {
		expectedID := "test-user-id"
		md := metadata.Pairs("x-user-id", expectedID)
		ctx := metadata.NewIncomingContext(context.Background(), md)
		id := GetUserIDFromContext(ctx)
		assert.Equal(t, expectedID, id)
	})
}

func TestHTTPHeaders(t *testing.T) {
	t.Run("GetOrGenerateTraceIDFromHeader", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		id1 := GetOrGenerateTraceIDFromHeader(req)
		assert.NotEmpty(t, id1)

		expectedID := "header-trace-id"
		req.Header.Set("X-Trace-Id", expectedID)
		id2 := GetOrGenerateTraceIDFromHeader(req)
		assert.Equal(t, expectedID, id2)
	})

	t.Run("GetUserIDFromHeader", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		assert.Empty(t, GetUserIDFromHeader(req))

		expectedID := "header-user-id"
		req.Header.Set("X-User-Id", expectedID)
		assert.Equal(t, expectedID, GetUserIDFromHeader(req))
	})

	t.Run("SetHeaders", func(t *testing.T) {
		w := httptest.NewRecorder()
		SetTraceIDHeader(w, "t-id")
		SetUserIDHeader(w, "u-id")

		assert.Equal(t, "t-id", w.Header().Get("X-Trace-Id"))
		assert.Equal(t, "u-id", w.Header().Get("X-User-Id"))
	})
}

func TestContextInjections(t *testing.T) {
	t.Run("TraceID Injection", func(t *testing.T) {
		ctx := context.Background()
		traceID := "trace-ctx-123"
		ctx = InjectTraceIDToContext(ctx, traceID)

		assert.Equal(t, traceID, GetTraceIDFromContext(ctx))
	})

	t.Run("UserID Injection", func(t *testing.T) {
		ctx := context.Background()
		userID := "user-ctx-123"
		ctx = InjectUserIDToContext(ctx, userID)

		assert.Equal(t, userID, GetUserIDFromContextGeneric(ctx))
	})
}
