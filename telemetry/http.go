package telemetry

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// HTTPHandler wraps a server handler: a server span per request (continuing
// the caller's traceparent), the standard HTTP server metrics, and a
// trace-aware logger in the request context (see WithTraceLogger).
//
// Wrap the mux directly: when it's a net/http ServeMux, the span is renamed
// to the matched pattern after routing ("GET /orders/{id}"); otherwise it is
// "<METHOD> <operation>".
func HTTPHandler(h http.Handler, operation string) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(WithTraceLogger(r.Context()))
		h.ServeHTTP(w, r)
		// ServeMux sets Pattern on the request it was given, this one.
		if r.Pattern != "" {
			span := trace.SpanFromContext(r.Context())
			span.SetName(r.Pattern)
			span.SetAttributes(attribute.String("http.route", r.Pattern))
		}
	})
	return otelhttp.NewHandler(inner, operation,
		otelhttp.WithSpanNameFormatter(func(op string, r *http.Request) string {
			return r.Method + " " + op
		}),
	)
}

// Transport wraps an HTTP client transport (nil: http.DefaultTransport) so
// outgoing requests are client spans and carry traceparent, e.g.
//
//	client := &http.Client{Transport: telemetry.Transport(nil)}
//	middlewares.NewAuthMeFunc(authURL, client)
func Transport(rt http.RoundTripper) http.RoundTripper {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return otelhttp.NewTransport(rt)
}
