package audit

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/salahfarzin/utils/middlewares"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func TestAuditedMethod(t *testing.T) {
	cfg := newInterceptorConfig(nil)
	for _, m := range []string{"/p.S/UpdateProduct", "/p.S/Delete", "/p.S/SetPrice", "/p.S/AddItem", "/p.S/CreateOrder"} {
		assert.True(t, cfg.auditedMethod(m), m)
	}
	for _, m := range []string{"/p.S/GetProduct", "/p.S/ListProducts", "/p.S/Settings", "/p.S/Addresses", "/p.S/Updates"} {
		assert.False(t, cfg.auditedMethod(m), m)
	}
	explicit := newInterceptorConfig([]InterceptorOption{Methods("/p.S/GetSecret")})
	assert.True(t, explicit.auditedMethod("/p.S/GetSecret"))
	assert.False(t, explicit.auditedMethod("/p.S/UpdateProduct"), "Methods replaces the default")
}

// authOK accepts "good" (holds orders/view only); anything else is rejected.
func authOK(token string) (*middlewares.User, error) {
	if token != "good" {
		return nil, errors.New("invalid")
	}
	return &middlewares.User{ID: "7", Uuid: "u-1", Email: "jane@example.com",
		Permissions: middlewares.Permissions{"orders": {"view"}}}, nil
}

// grpcCall runs audit → auth → handler, the documented chain.
func grpcCall(t *testing.T, rec *Recorder, method, token string, handler grpc.UnaryHandler) error {
	t.Helper()
	md := metadata.Pairs("x-forwarded-for", "203.0.113.7, 10.0.0.1", "grpcgateway-user-agent", "Mozilla", "x-request-id", "req-1")
	if token != "" {
		md.Append("authorization", "Bearer "+token)
	}
	ctx := metadata.NewIncomingContext(context.Background(), md)

	auth := middlewares.AuthUnaryInterceptor(authOK,
		middlewares.OnAuthenticated(CaptureUser),
		middlewares.RequirePermissions(map[string]middlewares.Permission{
			"/orders.S/DeleteOrder": {Resource: "orders", Action: "delete"},
		}))
	// What grpc.ChainUnaryInterceptor(UnaryServerInterceptor(rec), auth) runs.
	_, err := UnaryServerInterceptor(rec)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method},
		func(ctx context.Context, req any) (any, error) {
			return auth(ctx, req, &grpc.UnaryServerInfo{FullMethod: method}, handler)
		})
	return err
}

func ok(context.Context, any) (any, error) { return "ok", nil }

func TestUnaryInterceptor(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)

	// A write that succeeds: recorded with the caller and request details.
	require.NoError(t, grpcCall(t, rec, "/orders.S/UpdateOrder", "good", ok))
	// A read: not recorded.
	require.NoError(t, grpcCall(t, rec, "/orders.S/GetOrder", "good", ok))
	// A write that fails in the handler.
	err := grpcCall(t, rec, "/orders.S/CancelOrder", "good", func(context.Context, any) (any, error) {
		return nil, status.Error(codes.FailedPrecondition, "already shipped")
	})
	require.Error(t, err)
	// Refused by RequirePermissions: denied, and still names the caller.
	require.Equal(t, codes.PermissionDenied, status.Code(grpcCall(t, rec, "/orders.S/DeleteOrder", "good", ok)))
	// No valid token on a write: denied, anonymous.
	require.Equal(t, codes.Unauthenticated, status.Code(grpcCall(t, rec, "/orders.S/UpdateOrder", "bad", ok)))
	// No token on a read: not worth an event.
	require.Equal(t, codes.Unauthenticated, status.Code(grpcCall(t, rec, "/orders.S/GetOrder", "", ok)))
	// The handler records its own event: the automatic one is skipped.
	require.NoError(t, grpcCall(t, rec, "/orders.S/UpdateOrder", "good", func(ctx context.Context, _ any) (any, error) {
		rec.Record(ctx, "order.update", Resource{Type: "order", ID: "5"}, Before(map[string]int{"q": 1}), After(map[string]int{"q": 2}))
		return "ok", nil
	}))
	closeRecorder(t, rec)

	evs := pub.events(t)
	require.Len(t, evs, 5)
	jane := Actor{Type: ActorUser, ID: "u-1", Email: "jane@example.com", IP: "203.0.113.7", UserAgent: "Mozilla"}

	assert.Equal(t, "/orders.S/UpdateOrder", evs[0].Action)
	assert.Equal(t, Success, evs[0].Outcome)
	assert.Equal(t, jane, evs[0].Actor)
	assert.Equal(t, "req-1", evs[0].RequestID)
	assert.Equal(t, "OK", evs[0].Metadata["grpc.code"])

	assert.Equal(t, "/orders.S/CancelOrder", evs[1].Action)
	assert.Equal(t, Failure, evs[1].Outcome)
	assert.Equal(t, "already shipped", evs[1].Reason)

	assert.Equal(t, "/orders.S/DeleteOrder", evs[2].Action)
	assert.Equal(t, Denied, evs[2].Outcome)
	assert.Equal(t, jane, evs[2].Actor, "OnAuthenticated(CaptureUser) names who was refused")

	assert.Equal(t, Denied, evs[3].Outcome)
	assert.Equal(t, ActorAnonymous, evs[3].Actor.Type)
	assert.Equal(t, "203.0.113.7", evs[3].Actor.IP)

	assert.Equal(t, "order.update", evs[4].Action)
	assert.Equal(t, jane, evs[4].Actor)
	assert.Len(t, evs[4].Changes, 1)
}

func TestUnaryInterceptorPeerAddressAndSkip(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)
	ctx := peer.NewContext(context.Background(), &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("10.1.2.3"), Port: 5555}})

	call := func(method string, opts ...InterceptorOption) {
		_, err := UnaryServerInterceptor(rec, opts...)(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, ok)
		require.NoError(t, err)
	}
	call("/p.S/UpdateThing")
	call("/p.S/UpdateThing", Skip("/p.S/UpdateThing"))
	closeRecorder(t, rec)

	evs := pub.events(t)
	require.Len(t, evs, 1)
	assert.Equal(t, "10.1.2.3", evs[0].Actor.IP)
}

func TestHTTPMiddleware(t *testing.T) {
	pub := &memPublisher{}
	rec := newTestRecorder(t, pub)

	mux := http.NewServeMux()
	mux.HandleFunc("PUT /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := middlewares.RequirePermission(r.Context(), "orders", "edit"); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{}")) })
	mux.HandleFunc("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		rec.Record(r.Context(), "order.create", Resource{Type: "order", ID: "9"}, After(map[string]int{"q": 1}))
		w.WriteHeader(http.StatusCreated)
	})
	mux.HandleFunc("POST /health/ping", func(w http.ResponseWriter, _ *http.Request) {})

	h := HTTPMiddleware(rec, WrapHTTPAuth(middlewares.AuthMiddleware(authOK)), Skip("/health"))(mux)
	do := func(method, path, token string) int {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("User-Agent", "Mozilla")
		req.Header.Set("X-Forwarded-For", "203.0.113.7")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	assert.Equal(t, http.StatusForbidden, do("PUT", "/orders/5", "good")) // holds view, not edit
	assert.Equal(t, http.StatusOK, do("GET", "/orders/5", "good"))        // read: no event
	assert.Equal(t, http.StatusUnauthorized, do("PUT", "/orders/5", ""))  // write without token
	assert.Equal(t, http.StatusUnauthorized, do("GET", "/orders/5", ""))  // read without token: no event
	assert.Equal(t, http.StatusCreated, do("POST", "/orders", "good"))    // handler's own event only
	assert.Equal(t, http.StatusOK, do("POST", "/health/ping", "good"))    // skipped path
	closeRecorder(t, rec)

	evs := pub.events(t)
	require.Len(t, evs, 3)
	assert.Equal(t, "PUT /orders/5", evs[0].Action)
	assert.Equal(t, Denied, evs[0].Outcome)
	assert.Equal(t, Actor{Type: ActorUser, ID: "u-1", Email: "jane@example.com", IP: "203.0.113.7", UserAgent: "Mozilla"}, evs[0].Actor)
	assert.EqualValues(t, 403, evs[0].Metadata["http.status_code"])

	assert.Equal(t, Denied, evs[1].Outcome)
	assert.Equal(t, ActorAnonymous, evs[1].Actor.Type)

	assert.Equal(t, "order.create", evs[2].Action)
	assert.Equal(t, "u-1", evs[2].Actor.ID)
}
