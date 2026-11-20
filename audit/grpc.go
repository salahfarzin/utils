package audit

import (
	"context"
	"net"
	"strings"
	"unicode"

	"github.com/salahfarzin/utils/middlewares"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// writePrefixes are the method-name prefixes audited by default: a method is
// audited when its name is one of these followed by an upper-case letter or
// nothing ("UpdateProduct", "Delete"; not "Settings" or "GetAddresses").
var writePrefixes = []string{
	"Create", "Update", "Delete", "Set", "Add", "Remove", "Patch", "Upsert",
	"Assign", "Unassign", "Grant", "Revoke", "Cancel", "Approve", "Reject",
	"Change", "Reset", "Import", "Upload", "Move", "Archive", "Restore",
	"Enable", "Disable", "Publish", "Unpublish", "Send", "Register",
}

// InterceptorOption configures UnaryServerInterceptor and HTTPMiddleware.
type InterceptorOption func(*interceptorConfig)

type interceptorConfig struct {
	methods  map[string]bool
	skip     map[string]bool
	httpAuth middlewares.Middleware
}

// Methods audits exactly these full gRPC method names
// ("/product.ProductService/UpdateProduct") instead of the name-based default.
func Methods(fullMethods ...string) InterceptorOption {
	return func(c *interceptorConfig) {
		if c.methods == nil {
			c.methods = map[string]bool{}
		}
		for _, m := range fullMethods {
			c.methods[m] = true
		}
	}
}

// Skip never records automatic events for these full gRPC method names or
// HTTP path prefixes (denials included).
func Skip(methodsOrPaths ...string) InterceptorOption {
	return func(c *interceptorConfig) {
		if c.skip == nil {
			c.skip = map[string]bool{}
		}
		for _, m := range methodsOrPaths {
			c.skip[m] = true
		}
	}
}

// CaptureUser hands the caller auth resolved to the audit interceptor. Pass
// it to the auth interceptor, which runs after audit in the chain, so even a
// call auth then refuses (RequirePermissions) is recorded with who made it:
//
//	grpc.ChainUnaryInterceptor(
//	    audit.UnaryServerInterceptor(rec),
//	    middlewares.AuthUnaryInterceptor(authFn,
//	        middlewares.OnAuthenticated(audit.CaptureUser), ...),
//	)
func CaptureUser(ctx context.Context, user *middlewares.User) {
	if st := stateFrom(ctx); st != nil && user != nil {
		st.setUser(user)
	}
}

// WrapHTTPAuth runs the REST auth middleware (middlewares.AuthMiddleware)
// inside HTTPMiddleware, so audit sees its 401s and the user it resolved.
func WrapHTTPAuth(auth middlewares.Middleware) InterceptorOption {
	return func(c *interceptorConfig) { c.httpAuth = auth }
}

func newInterceptorConfig(opts []InterceptorOption) *interceptorConfig {
	c := &interceptorConfig{}
	for _, o := range opts {
		o(c)
	}
	return c
}

func (c *interceptorConfig) auditedMethod(fullMethod string) bool {
	if c.methods != nil {
		return c.methods[fullMethod]
	}
	name := fullMethod[strings.LastIndex(fullMethod, "/")+1:]
	for _, p := range writePrefixes {
		if rest, ok := strings.CutPrefix(name, p); ok && (rest == "" || unicode.IsUpper(rune(rest[0]))) {
			return true
		}
	}
	return false
}

// UnaryServerInterceptor records, for every gRPC call:
//   - denials: PermissionDenied always, Unauthenticated on audited methods;
//   - audited methods (writes, see Methods) as success or failure;
//
// and gives handlers' Record calls the caller's IP, user agent, Referer and
// request id.
// If a handler calls Record itself, the automatic event is skipped, so the
// handler's (with before/after) is the only one.
//
// Put it before the auth interceptor in the chain, and give auth
// middlewares.OnAuthenticated(CaptureUser) so events name the caller (see
// CaptureUser).
func UnaryServerInterceptor(rec *Recorder, opts ...InterceptorOption) grpc.UnaryServerInterceptor {
	cfg := newInterceptorConfig(opts)
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if cfg.skip[info.FullMethod] {
			return handler(ctx, req)
		}
		st := grpcRequestState(ctx)
		ctx = withState(ctx, st)
		resp, err := handler(ctx, req)

		code := status.Code(err)
		if outcome, ok := autoOutcome(cfg.auditedMethod(info.FullMethod), code == codes.Unauthenticated, code == codes.PermissionDenied, err != nil); ok {
			if _, _, _, _, recorded := st.snapshot(); !recorded {
				rec.Record(ctx, info.FullMethod, Resource{},
					Result(outcome, status.Convert(err).Message()),
					Meta("grpc.code", code.String()))
			}
		}
		return resp, err
	}
}

// autoOutcome decides whether a call gets an automatic event, and its outcome.
func autoOutcome(audited, unauthenticated, permissionDenied, failed bool) (Outcome, bool) {
	switch {
	case permissionDenied, unauthenticated && audited:
		return Denied, true
	case !audited:
		return "", false
	case failed:
		return Failure, true
	default:
		return Success, true
	}
}

func grpcRequestState(ctx context.Context) *requestState {
	st := &requestState{}
	md, _ := metadata.FromIncomingContext(ctx)
	first := func(keys ...string) string {
		for _, k := range keys {
			if v := md.Get(k); len(v) > 0 && v[0] != "" {
				return v[0]
			}
		}
		return ""
	}
	// grpc-gateway forwards the client's address, user agent and Referer
	// under these.
	if fwd := first("x-forwarded-for"); fwd != "" {
		st.ip = strings.TrimSpace(strings.Split(fwd, ",")[0])
	} else if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		st.ip = hostOnly(p.Addr.String())
	}
	st.userAgent = first("grpcgateway-user-agent", "user-agent")
	st.referer = cleanReferer(first("grpcgateway-referer", "referer"))
	st.requestID = first("x-request-id")
	return st
}

func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}
