// Package telemetry sets up OpenTelemetry for a service: traces and metrics
// exported over OTLP to the observability stack, W3C trace context
// propagation, and trace ids on every log line. See docs/telemetry.md.
package telemetry

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/salahfarzin/utils/internal/safelog"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
)

// ScopeName is the instrumentation scope of spans this package starts itself.
const ScopeName = "github.com/salahfarzin/utils/telemetry"

// Option configures Init.
type Option func(*config)

type config struct {
	version        string
	runtimeMetrics bool
}

// WithVersion sets service.version (e.g. the build's git tag).
func WithVersion(v string) Option { return func(c *config) { c.version = v } }

// WithoutRuntimeMetrics skips the Go runtime metrics (memory, GC, goroutines).
func WithoutRuntimeMetrics() Option { return func(c *config) { c.runtimeMetrics = false } }

// Shutdown flushes buffered telemetry and stops the exporters.
type Shutdown func(context.Context) error

// Init sets the global tracer and meter providers and the W3C propagator.
// Call it once at startup and defer the returned Shutdown.
//
// Configuration is the standard OpenTelemetry environment:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT  http://fluent-bit:4318 (no endpoint: nothing is exported,
//	                             but spans still get ids, so logs still carry trace_id)
//	OTEL_SERVICE_NAME            overrides service
//	OTEL_RESOURCE_ATTRIBUTES     e.g. deployment.environment.name=prod
//	OTEL_TRACES_SAMPLER          default parentbased_always_on; leave it, the collector samples
//	OTEL_SDK_DISABLED=true       propagation only
func Init(ctx context.Context, service string, opts ...Option) (Shutdown, error) {
	if service == "" {
		return nil, errors.New("telemetry: service name is required")
	}
	cfg := config{runtimeMetrics: true}
	for _, o := range opts {
		o(&cfg)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) {
		safelog.Get().Warn("telemetry: export problem", zap.Error(err))
	}))
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") {
		return func(context.Context) error { return nil }, nil
	}

	attrs := []attribute.KeyValue{attribute.String("service.name", service)}
	if cfg.version != "" {
		attrs = append(attrs, attribute.String("service.version", cfg.version))
	}
	res, err := resource.New(ctx,
		resource.WithAttributes(attrs...),
		resource.WithFromEnv(), // OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES win
		resource.WithTelemetrySDK(),
		resource.WithHost(),
	)
	if err != nil {
		return nil, err
	}

	var shutdowns []Shutdown
	shutdown := func(ctx context.Context) error {
		var errs []error
		for i := len(shutdowns) - 1; i >= 0; i-- {
			errs = append(errs, shutdowns[i](ctx))
		}
		return errors.Join(errs...)
	}

	tpOpts := []sdktrace.TracerProviderOption{sdktrace.WithResource(res)}
	if exporting("TRACES") {
		exp, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, err
		}
		tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)
	otel.SetTracerProvider(tp)
	shutdowns = append(shutdowns, tp.Shutdown)

	if exporting("METRICS") {
		exp, err := otlpmetrichttp.New(ctx)
		if err != nil {
			return nil, errors.Join(err, shutdown(ctx))
		}
		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(15*time.Second))),
		)
		otel.SetMeterProvider(mp)
		shutdowns = append(shutdowns, mp.Shutdown)
		if cfg.runtimeMetrics {
			if err := runtime.Start(runtime.WithMeterProvider(mp)); err != nil {
				return nil, errors.Join(err, shutdown(ctx))
			}
		}
	}
	return shutdown, nil
}

// exporting reports whether an OTLP endpoint is configured for signal
// ("TRACES" or "METRICS"), generally or for that signal.
func exporting(signal string) bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" ||
		os.Getenv("OTEL_EXPORTER_OTLP_"+signal+"_ENDPOINT") != ""
}
