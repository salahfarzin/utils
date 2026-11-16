# telemetry

OpenTelemetry for a service in a few lines: traces and metrics go over OTLP to the
observability stack (Fluent Bit → OTel Collector → Tempo / Prometheus), trace context
flows through HTTP, gRPC and Kafka, and every `logger.FromContext(ctx)` line carries
the request's `trace_id` and `span_id`, so Grafana links logs and traces.
How the stack fits together: the observability repo, `docs/architecture.md`.

## Wiring a service

```go
shutdown, err := telemetry.Init(ctx, "content-manager", telemetry.WithVersion(version))
if err != nil { log.Fatal(err) }
defer shutdown(context.Background()) // flushes spans and metrics

// gRPC server: spans per call, RPC metrics, trace-aware logger in the context.
grpcServer := grpc.NewServer(append(telemetry.ServerOptions(),
    grpc.ChainUnaryInterceptor(audit.UnaryServerInterceptor(rec), authInterceptor),
)...)

// gRPC clients, including grpc-gateway's connection to its own server.
conn, err := grpc.NewClient(addr, append(telemetry.DialOptions(), creds)...)

// REST: wrap the mux itself (a net/http ServeMux gets route-named spans).
handler := telemetry.HTTPHandler(mux, "content-manager-rest")

// Outgoing HTTP, e.g. auth's /auth/me.
client := &http.Client{Transport: telemetry.Transport(nil), Timeout: 5 * time.Second}
authFn := middlewares.NewAuthMeFunc(authURL, client)
```

Configuration is the standard OpenTelemetry environment:

| Variable | Value |
|---|---|
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `http://fluent-bit:4318` in compose, `http://localhost:4318` from the host. Unset: nothing is exported, but spans still get ids, so logs still carry `trace_id`. |
| `OTEL_SERVICE_NAME` | Overrides the name passed to `Init`. Keep it equal to the compose service name: that's the `service_name` label in Loki and Prometheus. |
| `OTEL_RESOURCE_ATTRIBUTES` | e.g. `deployment.environment.name=prod` |
| `OTEL_TRACES_SAMPLER` | Leave unset (`parentbased_always_on`). The Collector tail-samples; sampling here too would make the span metrics undercount. |
| `OTEL_SDK_DISABLED` | `true`: propagation only, no SDK. |

`Init` also exports Go runtime metrics (memory, GC, goroutines); `WithoutRuntimeMetrics()`
turns them off.

## What you get

- **HTTP** (`HTTPHandler`): a server span per request, continuing the caller's
  `traceparent`. Behind a `net/http` `ServeMux` it's renamed to the matched pattern
  (`GET /orders/{id}`) with `http.route` set; otherwise it's `<METHOD> <operation>`.
  Plus the standard `http.server.*` metrics.
- **gRPC** (`ServerOptions`, `DialOptions`): server and client spans named by method
  (`orders.OrderService/UpdateOrder`), parented across the call, plus `rpc.*` metrics.
- **Kafka**: `utils/kafka`'s `Producer.Produce` puts `traceparent` in the message
  headers, and its consumer loop runs each handler in a consumer span
  (`<topic> process`) continuing the producer's trace. Handlers get that context, so
  their logs and further calls join the same trace. For your own kafka-go code, use
  `InjectKafka` and `StartKafkaConsumer`/`EndSpan`.
- **Logs**: inside those handlers, `logger.FromContext(ctx)` returns a logger with
  `trace_id` and `span_id`. In jobs or consumers you write yourself, start a span, then
  call `ctx = telemetry.WithTraceLogger(ctx)`.

## Logs must go to stdout

Fluent Bit collects container stdout. The `logger` module currently writes to files by
default (`logs/<date>.log` in v0.x; a daily-rotated directory in v1.x), so in a
container its lines never reach Loki. Until `logger` can be pointed at stdout:

- `logger` v0.x: set `LOG_PATH=stdout`.
- `logger` v1.x: no stdout option yet. Tracked in PBI #790.

## Python services

Same stack, same environment variables, no code: add `opentelemetry-distro` and
`opentelemetry-exporter-otlp-proto-http`, run `opentelemetry-bootstrap -a install` once
at build time, and start with `opentelemetry-instrument python main.py`. FastAPI, gRPC
and HTTP clients are instrumented, and `traceparent` crosses into the Go services and
back.
