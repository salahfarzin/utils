package audit

import (
	"net/http"
	"strings"

	"github.com/salahfarzin/utils/middlewares"
)

// HTTPMiddleware is UnaryServerInterceptor for plain REST services: it
// records denials (403 always, 401 on writes) and every POST, PUT, PATCH and
// DELETE as success (< 400) or failure, and gives handlers' Record calls the
// caller's IP, user agent, Referer and X-Request-Id. Wrap the auth middleware with
// WrapHTTPAuth so audit knows who was denied.
//
// For grpc-gateway services use only UnaryServerInterceptor: the gateway's
// REST calls reach it as gRPC calls, and both would record them.
func HTTPMiddleware(rec *Recorder, opts ...InterceptorOption) middlewares.Middleware {
	cfg := newInterceptorConfig(opts)
	return func(next http.Handler) http.Handler {
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if st := stateFrom(r.Context()); st != nil {
				if u, ok := middlewares.GetUser(r.Context()); ok {
					st.setUser(u)
				}
			}
			next.ServeHTTP(w, r)
		})
		var chain http.Handler = inner
		if cfg.httpAuth != nil {
			chain = cfg.httpAuth(inner)
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if cfg.skippedPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			st := httpRequestState(r)
			ctx := withState(r.Context(), st)
			sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
			chain.ServeHTTP(sw, r.WithContext(ctx))

			audited := isWrite(r.Method)
			if outcome, ok := autoOutcome(audited, sw.code == http.StatusUnauthorized, sw.code == http.StatusForbidden, sw.code >= 400); ok {
				if _, _, _, _, recorded := st.snapshot(); !recorded {
					reason := ""
					if sw.code >= 400 {
						reason = http.StatusText(sw.code)
					}
					rec.Record(ctx, r.Method+" "+r.URL.Path, Resource{},
						Result(outcome, reason),
						Meta("http.status_code", sw.code))
				}
			}
		})
	}
}

func (c *interceptorConfig) skippedPath(path string) bool {
	for prefix := range c.skip {
		if strings.HasPrefix(prefix, "/") && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func httpRequestState(r *http.Request) *requestState {
	st := &requestState{
		userAgent: r.UserAgent(),
		referer:   cleanReferer(r.Referer()),
		requestID: r.Header.Get("X-Request-Id"),
	}
	switch {
	case r.Header.Get("X-Forwarded-For") != "":
		st.ip = strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	case r.Header.Get("X-Real-Ip") != "":
		st.ip = r.Header.Get("X-Real-Ip")
	default:
		st.ip = hostOnly(r.RemoteAddr)
	}
	return st
}

// statusWriter remembers the response status.
type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.code, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach Flush, Hijack, etc.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
