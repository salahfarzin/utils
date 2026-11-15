package telemetry

import (
	"context"

	"github.com/salahfarzin/logger"
	"github.com/salahfarzin/utils/internal/safelog"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// WithTraceLogger returns ctx carrying a logger whose lines include the
// current span's trace_id and span_id, so logger.FromContext(ctx) lines can be
// found from the trace in Grafana and vice versa. The HTTP handler and gRPC
// options here do this per request; call it yourself after starting a span
// in a job or consumer.
func WithTraceLogger(ctx context.Context) context.Context {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return ctx
	}
	base := logger.FromContext(ctx)
	if base == zap.L() { // nothing stored: FromContext fell back to zap's global
		base = safelog.Get()
	}
	return logger.WithLogger(ctx, base.With(
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	))
}
