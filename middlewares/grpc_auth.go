package middlewares

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// GRPCAuthOption configures AuthUnaryInterceptor and AuthStreamInterceptor.
type GRPCAuthOption func(*grpcAuth)

type grpcAuth struct {
	authenticate AuthServiceFunc
	public       []string
	required     map[string]Permission
}

// PublicMethods leaves methods open to callers without a token. Each entry is a
// full method ("/pkg.Service/Method") or a service ("/pkg.Service/"). A public
// call still has a verified caller in the context when it sends a valid token.
func PublicMethods(methods ...string) GRPCAuthOption {
	return func(a *grpcAuth) { a.public = append(a.public, methods...) }
}

// RequirePermissions refuses a call with PermissionDenied unless the caller
// holds the permission listed for its full method. Methods not listed need
// only a valid token; check finer rules in the handler with RequirePermission.
func RequirePermissions(byMethod map[string]Permission) GRPCAuthOption {
	return func(a *grpcAuth) {
		if a.required == nil {
			a.required = map[string]Permission{}
		}
		for m, p := range byMethod {
			a.required[m] = p
		}
	}
}

// AuthUnaryInterceptor authenticates gRPC calls itself, so a call made straight
// to the gRPC port is checked like one through the REST gateway: it resolves
// the access token from the "authorization: Bearer" metadata (which the
// gateway forwards) or the access_token cookie, and puts the user in the
// context for GetUser and GetUserFromContext.
//
// Incoming x-user-* metadata is always dropped. Those are the headers
// AuthMiddleware forwards to the gRPC handlers behind it; on the gRPC port
// anyone can set them, so behind this interceptor they never name the caller.
func AuthUnaryInterceptor(authenticate AuthServiceFunc, opts ...GRPCAuthOption) grpc.UnaryServerInterceptor {
	a := newGRPCAuth(authenticate, opts)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := a.check(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// AuthStreamInterceptor is AuthUnaryInterceptor for streaming calls.
func AuthStreamInterceptor(authenticate AuthServiceFunc, opts ...GRPCAuthOption) grpc.StreamServerInterceptor {
	a := newGRPCAuth(authenticate, opts)
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := a.check(ss.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		return handler(srv, &authedStream{ServerStream: ss, ctx: ctx})
	}
}

func newGRPCAuth(authenticate AuthServiceFunc, opts []GRPCAuthOption) *grpcAuth {
	a := &grpcAuth{authenticate: authenticate}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// check returns ctx with forged identity dropped and the verified caller added.
func (a *grpcAuth) check(ctx context.Context, fullMethod string) (context.Context, error) {
	ctx = withoutUserMetadata(ctx)
	public := a.isPublic(fullMethod)

	token := tokenFromMetadata(ctx)
	if token == "" {
		if public {
			return ctx, nil
		}
		return nil, status.Error(codes.Unauthenticated, "missing access token")
	}
	user, err := a.authenticate(token)
	if err != nil || user == nil {
		if public {
			return ctx, nil
		}
		return nil, status.Error(codes.Unauthenticated, "invalid access token")
	}
	ctx = context.WithValue(ctx, UserKey, user)

	if p, ok := a.required[fullMethod]; ok && !user.Can(p.Resource, p.Action) {
		return nil, status.Errorf(codes.PermissionDenied, "requires %s on %s", p.Action, p.Resource)
	}
	return ctx, nil
}

func (a *grpcAuth) isPublic(fullMethod string) bool {
	for _, m := range a.public {
		if fullMethod == m || (strings.HasSuffix(m, "/") && strings.HasPrefix(fullMethod, m)) {
			return true
		}
	}
	return false
}

// tokenFromMetadata finds the access token the way ExtractToken does for
// HTTP: a bearer authorization, then the access_token cookie (forwarded by
// the REST gateway as grpcgateway-cookie).
func tokenFromMetadata(ctx context.Context) string {
	md, _ := metadata.FromIncomingContext(ctx)
	for _, v := range md.Get("authorization") {
		if token, ok := strings.CutPrefix(v, "Bearer "); ok && token != "" {
			return token
		}
	}
	for _, key := range []string{"grpcgateway-cookie", "cookie"} {
		for _, header := range md.Get(key) {
			for _, part := range strings.Split(header, ";") {
				if token, ok := strings.CutPrefix(strings.TrimSpace(part), "access_token="); ok && token != "" {
					return token
				}
			}
		}
	}
	return ""
}

func withoutUserMetadata(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	clean := metadata.MD{}
	for k, v := range md {
		if !strings.HasPrefix(k, "x-user-") {
			clean[k] = v
		}
	}
	return metadata.NewIncomingContext(ctx, clean)
}

type authedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authedStream) Context() context.Context { return s.ctx }
