package telemetry

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/salahfarzin/logger"
	kafkago "github.com/segmentio/kafka-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/test/bufconn"
)

// recordSpans installs an in-memory tracer provider for one test.
func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTracerProvider(prevTP); otel.SetTextMapPropagator(prevProp) })
	return sr
}

// observedLogger returns a context carrying a logger whose entries the test
// can read.
func observedLogger(ctx context.Context) (context.Context, *observer.ObservedLogs) {
	core, logs := observer.New(zap.InfoLevel)
	return logger.WithLogger(ctx, zap.New(core)), logs
}

func fields(e observer.LoggedEntry) map[string]any { return e.ContextMap() }

func TestInitValidates(t *testing.T) {
	_, err := Init(context.Background(), "")
	assert.Error(t, err)
}

func TestInitWithoutEndpointStillGivesIDs(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := Init(context.Background(), "svc")
	require.NoError(t, err)
	defer func() { require.NoError(t, shutdown(context.Background())) }()

	_, span := otel.Tracer("t").Start(context.Background(), "op")
	defer span.End()
	assert.True(t, span.SpanContext().IsValid(), "logs get a trace_id even with nothing exported")
	assert.Contains(t, otel.GetTextMapPropagator().Fields(), "traceparent")
}

func TestInitDisabled(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")
	shutdown, err := Init(context.Background(), "svc")
	require.NoError(t, err)
	assert.NoError(t, shutdown(context.Background()))
}

func TestInitExportsOTLP(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.URL.Path] = r.Header.Get("Content-Type")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", srv.URL)
	t.Setenv("OTEL_SERVICE_NAME", "")

	shutdown, err := Init(context.Background(), "svc", WithVersion("v1.2.3"))
	require.NoError(t, err)
	_, span := otel.Tracer("t").Start(context.Background(), "op")
	span.End()
	require.NoError(t, shutdown(context.Background()), "shutdown flushes")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "application/x-protobuf", got["/v1/traces"])
	assert.Equal(t, "application/x-protobuf", got["/v1/metrics"], "runtime metrics")
}

func TestHTTPHandler(t *testing.T) {
	sr := recordSpans(t)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		logger.FromContext(r.Context()).Info("loading order")
		w.WriteHeader(http.StatusOK)
	})
	h := HTTPHandler(mux, "orders-api")

	parentTrace := "4bf92f3577b34da6a3ce929d0e0e4736"
	req := httptest.NewRequest("GET", "/orders/5", nil)
	req.Header.Set("traceparent", "00-"+parentTrace+"-00f067aa0ba902b7-01")
	ctx, logs := observedLogger(req.Context())
	h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	spans := sr.Ended()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /orders/{id}", spans[0].Name(), "renamed to the route after routing")
	assert.Equal(t, parentTrace, spans[0].SpanContext().TraceID().String(), "continues the caller's trace")
	assert.Equal(t, trace.SpanKindServer, spans[0].SpanKind())

	require.Equal(t, 1, logs.Len())
	assert.Equal(t, parentTrace, fields(logs.All()[0])["trace_id"])
	assert.Equal(t, spans[0].SpanContext().SpanID().String(), fields(logs.All()[0])["span_id"])
}

func TestHTTPHandlerWithoutServeMux(t *testing.T) {
	sr := recordSpans(t)
	h := HTTPHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), "gateway")
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/x", nil))
	assert.Equal(t, "POST gateway", sr.Ended()[0].Name())
}

func TestTransportPropagates(t *testing.T) {
	sr := recordSpans(t)
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("traceparent")
	}))
	defer srv.Close()

	ctx, parent := otel.Tracer("t").Start(context.Background(), "caller")
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
	resp, err := (&http.Client{Transport: Transport(nil)}).Do(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	parent.End()

	assert.Contains(t, seen, parent.SpanContext().TraceID().String())
	assert.Len(t, sr.Ended(), 2, "caller span + client span")
}

func TestGRPCClientAndServer(t *testing.T) {
	sr := recordSpans(t)
	lis := bufconn.Listen(1 << 20)
	var logged map[string]any
	srv := grpc.NewServer(append(ServerOptions(), grpc.ChainUnaryInterceptor(
		func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
			ctx, logs := observedLogger(ctx)
			ctx = WithTraceLogger(ctx)
			logger.FromContext(ctx).Info("in handler")
			logged = fields(logs.All()[0])
			return h(ctx, req)
		}))...)
	healthpb.RegisterHealthServer(srv, health.NewServer())
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet", append(DialOptions(),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))...)
	require.NoError(t, err)
	defer conn.Close()

	_, err = healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(sr.Ended()) == 2 }, time.Second, 10*time.Millisecond)

	byKind := map[trace.SpanKind]sdktrace.ReadOnlySpan{}
	for _, s := range sr.Ended() {
		byKind[s.SpanKind()] = s
	}
	client, server := byKind[trace.SpanKindClient], byKind[trace.SpanKindServer]
	require.NotNil(t, client)
	require.NotNil(t, server)
	assert.Equal(t, "grpc.health.v1.Health/Check", server.Name())
	assert.Equal(t, client.SpanContext().TraceID(), server.SpanContext().TraceID(), "one trace across the call")
	assert.Equal(t, client.SpanContext().SpanID(), server.Parent().SpanID())
	assert.Equal(t, server.SpanContext().TraceID().String(), logged["trace_id"])
}

func TestKafkaPropagation(t *testing.T) {
	sr := recordSpans(t)
	ctx, producer := otel.Tracer("t").Start(context.Background(), "produce")
	msg := kafkago.Message{Topic: "orders", Partition: 2, Offset: 41,
		Headers: []kafkago.Header{{Key: "traceparent", Value: []byte("stale")}}}
	InjectKafka(ctx, &msg)
	producer.End()
	assert.Len(t, msg.Headers, 1, "an existing traceparent is replaced, not duplicated")

	cctx, logs := observedLogger(context.Background())
	cctx, span := StartKafkaConsumer(cctx, msg)
	logger.FromContext(cctx).Info("handling")
	EndSpan(span, errors.New("bad payload"))

	consumer := sr.Ended()[1]
	assert.Equal(t, "orders process", consumer.Name())
	assert.Equal(t, trace.SpanKindConsumer, consumer.SpanKind())
	assert.Equal(t, producer.SpanContext().TraceID(), consumer.SpanContext().TraceID())
	assert.Equal(t, producer.SpanContext().SpanID(), consumer.Parent().SpanID())
	assert.Equal(t, codes.Error, consumer.Status().Code)
	assert.Equal(t, consumer.SpanContext().TraceID().String(), fields(logs.All()[0])["trace_id"])
}

func TestWithTraceLoggerWithoutSpan(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, ctx, WithTraceLogger(ctx), "no span: context unchanged")
}
